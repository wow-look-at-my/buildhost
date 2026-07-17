package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

var apiTracer = otel.Tracer("buildhost.api")

func init() {
	auth.OnReady(func() {
		auth.Handle("PUT /api/v1/projects/{project}/releases/{version}/artifacts/{os}/{arch}",
			parseRoute, handler.UploadArtifact)
	})
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

// maxUploadSize caps a single REST artifact upload. It is read once from config
// (BUILDHOST_MAX_UPLOAD_SIZE) so the limit is tunable rather than hardcoded.
var maxUploadSize = config.MaxUploadSize()

// Multi-platform alias expansions for the {os} and {arch} upload path
// segments. A Cosmopolitan/APE binary is one file that runs on every desktop
// OS, so "cosmo" (and the synonyms) publishes it for the canonical
// linux/darwin/windows set in one request; "any"/"all" in {arch} covers both
// mainstream CPU architectures. The expansion happens entirely at publish
// time -- each combination becomes an ordinary per-platform artifact row, all
// sharing the same content-addressed blob -- so downloads, latest-resolution,
// format handlers, and retention are untouched.
//
// The wasm platform is deliberately absent from both expansions: "any"/"all"
// mean "runs on every native desktop platform", and a wasm module is not that
// -- it needs a JS host or WASI runtime, and a native binary cannot run under
// one. Publish wasm explicitly (os=wasm, arch=js/wasip1).
var (
	osAliasExpansion   = []db.OS{db.OSLinux, db.OSDarwin, db.OSWindows}
	archAliasExpansion = []db.Arch{db.ArchAMD64, db.ArchARM64}
)

// expandOSSpec parses the {os} path segment of an artifact upload: a single
// OS name (any spelling db.NormalizeOS accepts), a comma-separated list of
// them, or an expand-everywhere alias (cosmo/any/all/universal). It rejects
// unknown names, empty elements, and duplicates (after normalization, so
// "macos,darwin" is a duplicate too) with an error suitable for a 400 body.
func expandOSSpec(spec string) ([]db.OS, error) {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "cosmo", "any", "all", "universal":
		return osAliasExpansion, nil
	}

	elems := strings.Split(spec, ",")
	out := make([]db.OS, 0, len(elems))
	seen := make(map[db.OS]bool, len(elems))
	for _, elem := range elems {
		osName, ok := db.NormalizeOS(elem)
		if !ok {
			return nil, fmt.Errorf("invalid os %q", strings.TrimSpace(elem))
		}
		if seen[osName] {
			return nil, fmt.Errorf("duplicate os %q", osName)
		}
		seen[osName] = true
		out = append(out, osName)
	}
	return out, nil
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
	seen := make(map[db.Arch]bool, len(elems))
	for _, elem := range elems {
		arch, ok := db.NormalizeArch(elem)
		if !ok {
			return nil, fmt.Errorf("invalid arch %q", strings.TrimSpace(elem))
		}
		if seen[arch] {
			return nil, fmt.Errorf("duplicate arch %q", arch)
		}
		seen[arch] = true
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

	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = r.Header.Get("X-Artifact-Kind")
	}
	if kind == "" {
		kind = "binary"
	}
	if !db.ValidKind(kind) {
		jsonError(w, http.StatusBadRequest, "invalid kind")
		return
	}

	// The (os, arch) combinations this one upload publishes. An npm package
	// keeps the literal os=any/arch=any sentinel row it has always used; every
	// other kind goes through the multi-platform expansion (a single canonical
	// name stays exactly one combination, today's behavior).
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
		// name wasm artifacts GOOS_GOARCH (name_js_wasm / name_wasip1_wasm)
		// and upload with os=js/arch=wasm. Fold the pair to the canonical
		// os=wasm form at parse time -- "js" is never stored as an os.
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

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	hasher := sha256.New()
	body := io.TeeReader(r.Body, hasher)

	// The body is streamed to content-addressed storage exactly once no matter
	// how many combinations it fans out to; every artifact row references the
	// same blob (storage dedup makes the fan-out cost rows, not bytes).
	storageKey, size, err := h.Store.Put(ctx, body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "store failed")
		jsonError(w, http.StatusInternalServerError, "failed to store artifact")
		return
	}

	sha256hex := hex.EncodeToString(hasher.Sum(nil))
	span.SetAttributes(attribute.Int64("artifact.size", size))

	filename := sanitizeFilename(r.Header.Get("X-Artifact-Filename"))

	artifacts := make([]*db.Artifact, 0, len(oses)*len(arches))
	for _, osName := range oses {
		for _, arch := range arches {
			artifacts = append(artifacts, &db.Artifact{
				ReleaseID:  release.ID,
				OS:         osName,
				Arch:       arch,
				Kind:       db.Kind(kind),
				StorageKey: storageKey,
				Size:       size,
				SHA256:     sha256hex,
				Filename:   filename,
			})
		}
	}

	// Single combination keeps the exact pre-fan-out code path and response; a
	// multi-combination upload creates all rows atomically (any conflicting
	// combination fails the whole request with nothing created, so the client
	// can resolve it and retry the identical request).
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
				// already exists") -- a fan-out can conflict on any one of
				// several rows and the client needs to know which.
				msg = err.Error()
			}
			jsonError(w, http.StatusConflict, msg)
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to record artifact")
		return
	}

	if len(artifacts) == 1 {
		jsonResponse(w, http.StatusCreated, artifacts[0])
		return
	}
	jsonResponse(w, http.StatusCreated, artifacts)
}
