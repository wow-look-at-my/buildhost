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
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// manifestPassthroughFields are the package.json fields buildhost surfaces from
// a pre-built npm-package tarball into its packument version entry. They are the
// fields npm needs to resolve and gate a package -- its dependency graph and its
// platform/engine constraints -- plus bin. name/version/dist stay
// buildhost-authoritative and are never taken from the tarball; lifecycle fields
// (scripts) are intentionally omitted so serving a packument never implies
// running install hooks.
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

// npmManifestFields reads package/package.json from a stored npm-package tarball
// and returns the subset of fields (manifestPassthroughFields) buildhost echoes
// into the packument. Returns nil on any error -- missing blob, unreadable
// archive, bad JSON -- so the packument still serves a minimal-but-valid entry
// rather than failing the whole request.
func (h *Handler) npmManifestFields(ctx context.Context, storageKey string) map[string]any {
	rc, _, err := h.Store.Get(ctx, storageKey)
	if err != nil {
		return nil
	}
	defer rc.Close()

	pkg, err := readPackageJSONFromTarball(rc)
	if err != nil || pkg == nil {
		return nil
	}

	out := map[string]any{}
	for _, f := range manifestPassthroughFields {
		if v, ok := pkg[f]; ok {
			out[f] = v
		}
	}
	return out
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
	// projectToNPMName); decode happens at route parse, encode here to match.
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
