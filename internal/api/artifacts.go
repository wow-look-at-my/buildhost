package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/exeformat"
	"github.com/wow-look-at-my/buildhost/internal/uploads"
	"github.com/wow-look-at-my/go-containers/set"
)

var apiTracer = otel.Tracer("buildhost.api")

func init() {
	auth.HandlePrimary("PUT /api/v1/projects/{project}/releases/{version}/artifacts/{os}/{arch}",
		parseRoute, handler.UploadArtifact)
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = filepath.Base(name)
	if name == "." || name == "/" || name == ".." {
		return ""
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	if len(name) > 255 {
		name = name[:255]
	}
	return name
}

var maxUploadSize = config.MaxUploadSize()

var validSHA256Hex = regexp.MustCompile(`^[a-f0-9]{64}$`)

// hashRefRequest reports whether this request is a hash-reference upload: an
// upload_sha256 with no upload_session and a definitively empty body. A
// session finalize keeps its existing meaning for upload_sha256 (integrity
func hashRefRequest(r *http.Request) (string, bool) {
	q := r.URL.Query()
	ref := q.Get(uploads.SHA256Param)
	if ref == "" || q.Get(uploads.SessionParam) != "" || r.ContentLength != 0 {
		return "", false
	}
	return strings.ToLower(ref), true
}

// Multi-platform alias expansions for the {os} and {arch} upload path
var (
	osAliasExpansion   = []db.OS{db.OSLinux, db.OSDarwin, db.OSWindows}
	archAliasExpansion = []db.Arch{db.ArchAMD64, db.ArchARM64}
)

// expandOSSpec parses the {os} path segment of an artifact upload: a single
// OS name (any spelling db.NormalizeOS accepts), a comma-separated list of
// them, or an expand-everywhere alias (cosmo/any/all/universal). It rejects
// unknown names, empty elements, and duplicates (after normalization, so
func expandOSSpec(spec string) ([]db.OS, error) {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "cosmo", "any", "all", "universal":
		return osAliasExpansion, nil
	}

	elems := strings.Split(spec, ",")
	out := make([]db.OS, 0, len(elems))
	seen := set.New[db.OS](len(elems))
	for _, elem := range elems {
		osName, ok := db.NormalizeOS(elem)
		if !ok {
			return nil, fmt.Errorf("invalid os %q", strings.TrimSpace(elem))
		}
		if !seen.Add(osName) {
			return nil, fmt.Errorf("duplicate os %q", osName)
		}
		out = append(out, osName)
	}
	return out, nil
}

// platformCombos is the cartesian product the {os}/{arch} segments expand to,
// in the order the rows would have been created.
func platformCombos(oses []db.OS, arches []db.Arch) []db.Platform {
	out := make([]db.Platform, 0, len(oses)*len(arches))
	for _, osName := range oses {
		for _, arch := range arches {
			out = append(out, db.Platform{OS: osName, Arch: arch})
		}
	}
	return out
}

// expandArchSpec parses the {arch} path segment of an artifact upload: a
// single architecture name (any spelling db.NormalizeArch accepts), a
// comma-separated list, or "any"/"all" for the amd64+arm64 pair. Unknown
// names, empty elements, and duplicates are rejected like expandOSSpec.
func expandArchSpec(spec string) ([]db.Arch, error) {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "any", "all":
		return archAliasExpansion, nil
	}

	elems := strings.Split(spec, ",")
	out := make([]db.Arch, 0, len(elems))
	seen := set.New[db.Arch](len(elems))
	for _, elem := range elems {
		arch, ok := db.NormalizeArch(elem)
		if !ok {
			return nil, fmt.Errorf("invalid arch %q", strings.TrimSpace(elem))
		}
		if !seen.Add(arch) {
			return nil, fmt.Errorf("duplicate arch %q", arch)
		}
		out = append(out, arch)
	}
	return out, nil
}

func (h *Handler) UploadArtifact(w http.ResponseWriter, r *http.Request) {
	ctx, span := apiTracer.Start(r.Context(), "api.upload_artifact")
	defer span.End()

	project := auth.ProjectFrom(ctx)
	rt := routeFrom(ctx)

	span.SetAttributes(
		attribute.String("artifact.project", project.Name),
		attribute.String("artifact.version", rt.version),
		attribute.String("artifact.os", rt.os),
		attribute.String("artifact.arch", rt.arch),
	)

	release := h.getRelease(w, r, project.ID, rt.version)
	if release == nil {
		return
	}

	if release.Published {
		jsonError(w, http.StatusConflict, "release already published")
		return
	}

	kind := artifactKind(r)
	if !db.ValidKind(kind) {
		jsonError(w, http.StatusBadRequest, "invalid kind")
		return
	}

	var oses []db.OS
	var arches []db.Arch
	if kind == string(db.KindNPMPackage) {
		if rt.os != "any" || rt.arch != "any" {
			jsonError(w, http.StatusBadRequest, "npm-package artifacts must use os=any and arch=any")
			return
		}
		oses, arches = []db.OS{db.OS(rt.os)}, []db.Arch{db.Arch(rt.arch)}
	} else if o, a, ok := db.NormalizeLegacyWasmPair(rt.os, rt.arch); ok {
		// Deprecated legacy shim: currently-released go-toolchain autoreleases
		oses, arches = []db.OS{o}, []db.Arch{a}
	} else {
		var err error
		if oses, err = expandOSSpec(rt.os); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if arches, err = expandArchSpec(rt.arch); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Every combination the upload fans out to must be a coherent
		// platform: os=wasm pairs only with the wasm flavor arches
		// (js/wasip1) and vice versa -- a "linux/js" or "wasm/amd64" row
		// could never be downloaded by anything real. Checked before the
		// body is read so a bad spec stores nothing.
		for _, osName := range oses {
			for _, arch := range arches {
				if !db.CompatiblePlatform(osName, arch) {
					jsonError(w, http.StatusBadRequest, fmt.Sprintf(
						"incompatible os/arch pair %s/%s: os=wasm pairs only with arch js or wasip1 (and those arches only with os=wasm)",
						osName, arch))
					return
				}
			}
		}
	}

	stored, ok := h.storeUpload(ctx, w, r, project.ID)
	if !ok {
		return
	}
	span.SetAttributes(attribute.Int64("artifact.size", stored.size))

	filename := sanitizeFilename(r.Header.Get("X-Artifact-Filename"))

	if combos := platformCombos(oses, arches); len(combos) > 1 && stored.format.MultiPlatformCapable() {
		h.publishMultiPlatform(ctx, w, publishSpec{
			releaseID: release.ID,
			kind:      kind,
			filename:  filename,
			stored:    stored,
			platforms: combos,
			span:      span,
		})
		return
	}

	artifacts := make([]*db.Artifact, 0, len(oses)*len(arches))
	for _, osName := range oses {
		for _, arch := range arches {
			artifacts = append(artifacts, &db.Artifact{
				ReleaseID:  release.ID,
				OS:         osName,
				Arch:       arch,
				Kind:       db.Kind(kind),
				StorageKey: stored.storageKey,
				Size:       stored.size,
				SHA256:     stored.sha256hex,
				Filename:   filename,
				ExeFormat:  string(stored.format),
			})
		}
	}

	// Single combination keeps the exact pre-fan-out code path and response; a
	var err error
	if len(artifacts) == 1 {
		err = h.DB.CreateArtifact(ctx, artifacts[0])
	} else {
		err = h.DB.CreateArtifacts(ctx, artifacts)
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "create artifact failed")
		if errors.Is(err, db.ErrConflict) {
			msg := "artifact already exists for this os/arch"
			if len(artifacts) > 1 {
				// Name the conflicting combination ("artifact linux/amd64:
				msg = err.Error()
			}
			jsonError(w, http.StatusConflict, msg)
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to record artifact")
		return
	}

	withPlatforms := make([]db.ArtifactWithPlatforms, len(artifacts))
	for i, a := range artifacts {
		withPlatforms[i] = db.ArtifactWithPlatforms{
			Artifact:  *a,
			Platforms: []db.Platform{{OS: a.OS, Arch: a.Arch}},
		}
	}
	if len(withPlatforms) == 1 {
		jsonResponse(w, http.StatusCreated, withPlatforms[0])
		return
	}
	jsonResponse(w, http.StatusCreated, withPlatforms)
}

// storedUpload is the outcome of getting an upload's bytes into storage,
// whichever way they arrived (streamed body, chunked session, or a hash
// reference to a blob the project already has).
type storedUpload struct {
	storageKey string
	sha256hex  string
	size       int64
	// format is what the leading bytes say the file is. "" means unrecognized,
	format exeformat.Format
	// ntBoot says whether an APE's PE header can boot it on Windows, so a
	ntBoot exeformat.NTBoot
}

// storeUpload resolves an artifact upload's bytes to a stored blob. A
// hash-reference upload (empty body + upload_sha256) registers a blob the
// project already has; anything else streams the body to content-addressed
func (h *Handler) storeUpload(ctx context.Context, w http.ResponseWriter, r *http.Request, projectID int64) (storedUpload, bool) {
	if refHex, ok := hashRefRequest(r); ok {
		key, size, head, ok := h.resolveHashRef(ctx, w, projectID, refHex)
		if !ok {
			return storedUpload{}, false
		}
		return storedUpload{
			storageKey: key,
			sha256hex:  key,
			size:       size,
			format:     exeformat.Detect(head),
			ntBoot:     exeformat.DetectNTBoot(head),
		}, true
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	hasher := sha256.New()
	head := &headCapture{}
	body := io.TeeReader(io.TeeReader(r.Body, hasher), head)

	key, size, err := h.Store.Put(ctx, body)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to store artifact")
		return storedUpload{}, false
	}
	return storedUpload{
		storageKey: key,
		sha256hex:  hex.EncodeToString(hasher.Sum(nil)),
		size:       size,
		format:     exeformat.Detect(head.head),
		ntBoot:     exeformat.DetectNTBoot(head.head),
	}, true
}

type headCapture struct{ head []byte }

func (c *headCapture) Write(p []byte) (int, error) {
	if n := exeformat.SniffLen - len(c.head); n > 0 {
		c.head = append(c.head, p[:min(n, len(p))]...)
	}
	return len(p), nil
}

// resolveHashRef authorizes and resolves a hash-reference upload: the
// referenced blob must already belong to this project (any release -- which
// covers "the previous slot of this same release" as well as an unchanged
// binary from an earlier release) and must still exist in storage. It returns
// the storage key, the blob's decompressed size for the artifact row, and its
// leading bytes for the executable-format check; on failure it writes the error
// response and returns ok=false.
func (h *Handler) resolveHashRef(ctx context.Context, w http.ResponseWriter, projectID int64, refHex string) (string, int64, []byte, bool) {
	if !validSHA256Hex.MatchString(refHex) {
		jsonError(w, http.StatusBadRequest, "invalid upload_sha256: want 64 hex characters")
		return "", 0, nil, false
	}
	owned, err := h.DB.BlobBelongsToProject(ctx, projectID, refHex)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to look up blob")
		return "", 0, nil, false
	}
	exists := false
	if owned {
		// The blob may have been garbage-collected since its rows were
		// evicted; a dangling reference must miss, not create a broken row.
		if exists, err = h.Store.Exists(ctx, refHex); err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to look up blob")
			return "", 0, nil, false
		}
	}
	if !owned || !exists {
		jsonError(w, http.StatusNotFound, "no blob with this upload_sha256 in this project")
		return "", 0, nil, false
	}
	// Read the decompressed size from the blob header, plus enough leading
	rc, size, err := h.Store.Get(ctx, refHex)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to read blob")
		return "", 0, nil, false
	}
	defer rc.Close()
	head := make([]byte, exeformat.SniffLen)
	n, err := io.ReadFull(rc, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		jsonError(w, http.StatusInternalServerError, "failed to read blob")
		return "", 0, nil, false
	}
	return refHex, size, head[:n], true
}
