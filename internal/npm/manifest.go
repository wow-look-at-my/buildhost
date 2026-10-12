package npm

// Pre-built npm-package artifact support: reflecting the uploaded tarball's
// own package.json manifest into the packument, and serving the stored
// tarball itself.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// manifestPassthroughFields are the package.json fields buildhost surfaces from
// a pre-built npm-package tarball into its packument version entry. They are the
var manifestPassthroughFields = []string{
	"dependencies",
	"optionalDependencies",
	"peerDependencies",
	"peerDependenciesMeta",
	"bundleDependencies",
	"bundledDependencies",
	"bin",
	"os",
	"cpu",
	"engines",
}

const (
	// npmManifestCacheFormat is the packaged_artifacts format under which an
	npmManifestCacheFormat = "npm-manifest"

	// npmManifestFieldsVersion identifies the extraction contract -- which
	npmManifestFieldsVersion = 1
)

// npmManifestMetadata is the cached extraction result, stored in the row's
type npmManifestMetadata struct {
	FieldsVersion int            `json:"fields_version"`
	Fields        map[string]any `json:"fields"`
}

const (
	manifestFillConcurrency = 8

	manifestFillBudget = 20 * time.Second
)

// errManifestFillBudget reports that a packument could not resolve every
var errManifestFillBudget = errors.New("npm: manifest cache fill exceeded its budget")

// resolveNPMManifestFields returns the passthrough fields for each npm-package
func (h *Handler) resolveNPMManifestFields(ctx context.Context, artifacts []db.Artifact) (map[int64]map[string]any, error) {
	out := make(map[int64]map[string]any, len(artifacts))
	var misses []db.Artifact
	for _, a := range artifacts {
		if fields, ok := h.cachedNPMManifestFields(ctx, a.ID); ok {
			out[a.ID] = fields
			continue
		}
		misses = append(misses, a)
	}
	if len(misses) == 0 {
		return out, nil
	}
	slog.Info("npm: filling manifest cache", "artifacts", len(misses))

	budget := h.fillBudget
	if budget <= 0 {
		budget = manifestFillBudget
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	// Group the misses by blob. Storage is content-addressed, so a release
	byBlob := map[string][]db.Artifact{}
	var blobs []string
	for _, a := range misses {
		if _, seen := byBlob[a.StorageKey]; !seen {
			blobs = append(blobs, a.StorageKey)
		}
		byBlob[a.StorageKey] = append(byBlob[a.StorageKey], a)
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, manifestFillConcurrency)
	)
	for _, key := range blobs {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(key string, sharing []db.Artifact) {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			fields, cacheable, err := h.extractNPMManifestFields(ctx, key)
			if err != nil {
				// The budget check below is what turns a systemic stall into
				// an error. A single unreadable blob (evicted, storage error)
				if ctx.Err() == nil {
					slog.Warn("npm: read package manifest", "storage_key", key, "err", err)
					fields = map[string]any{}
				} else {
					return
				}
			}
			mu.Lock()
			for _, a := range sharing {
				out[a.ID] = fields
			}
			mu.Unlock()
			if cacheable {
				for _, a := range sharing {
					h.cacheNPMManifestFields(ctx, a, fields)
				}
			}
		}(key, byBlob[key])
	}
	wg.Wait()

	for _, a := range artifacts {
		if _, ok := out[a.ID]; !ok {
			return nil, errManifestFillBudget
		}
	}
	return out, nil
}

// cachedNPMManifestFields reports the artifact's cached fields. ok=false means
// the cache holds no usable answer (never filled, filled under an older
// extraction contract, or corrupt) -- distinct from a cached empty map, which
// is the final answer for a blob that is not a readable npm tarball.
func (h *Handler) cachedNPMManifestFields(ctx context.Context, artifactID int64) (map[string]any, bool) {
	_, _, _, _, metadata, err := h.DB.GetPackagedArtifact(ctx, artifactID, npmManifestCacheFormat)
	if err != nil {
		return nil, false
	}
	var m npmManifestMetadata
	if err := json.Unmarshal([]byte(metadata), &m); err != nil || m.FieldsVersion != npmManifestFieldsVersion {
		return nil, false
	}
	if m.Fields == nil {
		m.Fields = map[string]any{}
	}
	return m.Fields, true
}

// cacheNPMManifestFields fills the cache. Best-effort: the fields are already
// correct for this response, and the extraction is a pure function of a
// content-addressed blob, so a concurrent double-fill stores the same value.
func (h *Handler) cacheNPMManifestFields(ctx context.Context, a db.Artifact, fields map[string]any) {
	meta, err := json.Marshal(npmManifestMetadata{FieldsVersion: npmManifestFieldsVersion, Fields: fields})
	if err != nil {
		return
	}
	// storage_key/size mirror the source artifact exactly so retention's
	// freed-bytes UNION dedupes this row against the artifact's own.
	if err := h.DB.CreatePackagedArtifact(ctx, a.ID, npmManifestCacheFormat, a.StorageKey, a.Size, a.SHA256, "package.json", string(meta)); err != nil {
		slog.Warn("cache npm manifest fields", "artifact_id", a.ID, "err", err)
	}
}

// extractNPMManifestFields reads package/package.json out of a stored
// npm-package tarball and returns the subset of fields
// (manifestPassthroughFields) buildhost echoes into the packument.
//
// cacheable distinguishes a final answer from a transient failure. A blob that
// is readable but is not a usable npm tarball (not gzip, no package.json, bad
// JSON) yields an empty map with cacheable=true: that verdict can never change
// for a content-addressed blob, so caching it keeps the packument off the blob
func (h *Handler) extractNPMManifestFields(ctx context.Context, storageKey string) (fields map[string]any, cacheable bool, err error) {
	rc, _, err := h.Store.Get(ctx, storageKey)
	if err != nil {
		return nil, false, err
	}
	defer rc.Close()

	pkg, err := readPackageJSONFromTarball(rc)
	if err != nil || pkg == nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, false, ctxErr
		}
		return map[string]any{}, true, nil
	}

	out := map[string]any{}
	for _, f := range manifestPassthroughFields {
		if v, ok := pkg[f]; ok {
			out[f] = v
		}
	}
	return out, true, nil
}

// readPackageJSONFromTarball extracts and parses package/package.json from a
// gzipped npm tarball stream. The manifest read is capped to guard against a
// malicious or corrupt archive claiming a huge package.json.
func readPackageJSONFromTarball(r io.Reader) (map[string]any, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimPrefix(hdr.Name, "./") != "package/package.json" {
			continue
		}
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, io.LimitReader(tr, 1<<20)); err != nil {
			return nil, err
		}
		var pkg map[string]any
		if err := json.Unmarshal(buf.Bytes(), &pkg); err != nil {
			return nil, err
		}
		return pkg, nil
	}
}

func (h *Handler) serveTarball(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())
	ri := tarballRouteFrom(r.Context())
	filename := ri.filename

	// The tarball filename embeds the npm-encoded project name (see
	prefix := projectToNPMName(project.Name) + "-"
	if !strings.HasPrefix(filename, prefix) {
		http.NotFound(w, r)
		return
	}
	version := strings.TrimSuffix(filename[len(prefix):], ".tgz")
	if version == "" || version == filename[len(prefix):] {
		http.NotFound(w, r)
		return
	}

	release, err := h.DB.GetRelease(r.Context(), project.ID, version)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	artifact, err := h.DB.GetArtifactByKind(r.Context(), release.ID, db.KindNPMPackage)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	rc, size, err := h.Store.Get(r.Context(), artifact.StorageKey)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
	if size > 0 {
		w.Header().Set("Content-Length", fmt.Sprint(size))
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	io.Copy(w, rc)
}
