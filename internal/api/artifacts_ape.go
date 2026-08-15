package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/exeformat"
)

func init() {
	auth.HandlePrimary("PUT /api/v1/projects/{project}/releases/{version}/artifacts/ape",
		parseRoute, handler.UploadMultiPlatformArtifact)
}

// UploadMultiPlatformArtifact publishes ONE file that runs on several platforms
// as ONE artifact: one blob, one row, one download link, N occupied slots. The
// slot-per-platform fan-out on the {os}/{arch} route answers a different
// question -- N separate builds that happen to share bytes -- and is unchanged.
//
//	PUT .../artifacts/ape?platforms=linux/amd64,darwin/arm64,windows/amd64
//
// platforms[0] is the artifact's canonical slot. Every covered platform
// resolves to this artifact, so all of them redirect to one static URL with one
// digest and one ETag. See docs/multi-platform-artifacts.md.
func (h *Handler) UploadMultiPlatformArtifact(w http.ResponseWriter, r *http.Request) {
	ctx, span := apiTracer.Start(r.Context(), "api.upload_multi_platform_artifact")
	defer span.End()

	project := auth.ProjectFrom(ctx)
	rt := routeFrom(ctx)

	release := h.getRelease(w, r, project.ID, rt.version)
	if release == nil {
		return
	}
	if release.Published {
		jsonError(w, http.StatusConflict, "release already published")
		return
	}

	spec := r.URL.Query().Get("platforms")
	if spec == "" {
		jsonError(w, http.StatusBadRequest, "platforms is required: a comma-separated os/arch list, e.g. platforms=linux/amd64,darwin/arm64,windows/amd64")
		return
	}
	platforms, err := db.ParsePlatformList(spec)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	kind := artifactKind(r)
	if !db.ValidKind(kind) {
		jsonError(w, http.StatusBadRequest, "invalid kind")
		return
	}
	// A docker image is an OCI manifest with its own per-platform indexing, and
	// an npm package is a tarball pinned to the os=any/arch=any sentinel. Neither
	// is a single executable covering a platform set.
	if kind == string(db.KindDocker) || kind == string(db.KindNPMPackage) {
		jsonError(w, http.StatusBadRequest, "kind "+kind+" cannot be published as a multi-platform artifact")
		return
	}

	span.SetAttributes(
		attribute.String("artifact.project", project.Name),
		attribute.String("artifact.version", rt.version),
		attribute.String("artifact.platforms", db.FormatPlatforms(platforms)),
	)

	stored, ok := h.storeUpload(ctx, w, r, project.ID)
	if !ok {
		return
	}
	span.SetAttributes(
		attribute.Int64("artifact.size", stored.size),
		attribute.String("artifact.exe_format", string(stored.format)),
	)

	h.publishMultiPlatform(ctx, w, publishSpec{
		releaseID: release.ID,
		kind:      kind,
		filename:  sanitizeFilename(r.Header.Get("X-Artifact-Filename")),
		stored:    stored,
		platforms: platforms,
		span:      span,
	})
}

// publishSpec is one multi-platform publish: the stored blob plus what it is
// being claimed to cover.
type publishSpec struct {
	releaseID int64
	kind      string
	filename  string
	stored    storedUpload
	platforms []db.Platform
	span      trace.Span
}

// publishMultiPlatform records ONE artifact covering several platforms, after
// checking the claim against the bytes. Both publishing routes come through
// here -- the explicit /artifacts/ape endpoint and the {os}/{arch} route's
// alias expansions -- so one file is one row whichever spelling published it,
// and neither route can drift out of the gates below.
func (h *Handler) publishMultiPlatform(ctx context.Context, w http.ResponseWriter, spec publishSpec) {
	// A claim that one file runs on several platforms is only true for a format
	// that actually carries several platforms' code. Reject anything else here
	// rather than publish an unbacked claim -- the badge on the release page
	// reads straight off this.
	if len(spec.platforms) > 1 && !spec.stored.format.MultiPlatformCapable() {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf(
			"upload declares %d platforms but is not an Actually Portable Executable (no MZqFpD magic); "+
				"publish per-platform builds to /artifacts/{os}/{arch} instead", len(spec.platforms)))
		return
	}
	// A declared windows platform served by the do-nothing stub PE header is the
	// worst failure this endpoint can publish: the download succeeds, the binary
	// starts, and it exits 0 without running. Nothing downstream can tell that
	// apart from success, so refuse it at ingest.
	if spec.stored.ntBoot == exeformat.NTStub {
		if p, ok := firstWindows(spec.platforms); ok {
			jsonError(w, http.StatusBadRequest, fmt.Sprintf(
				"upload declares %s but its PE header is the do-nothing stub (one section), which maps none of the "+
					"payload: the binary would start on Windows and exit 0 without running. Build the APE with "+
					"windows support, or drop %s from platforms", p, p))
			return
		}
	}

	artifact := &db.Artifact{
		ReleaseID:  spec.releaseID,
		Kind:       db.Kind(spec.kind),
		StorageKey: spec.stored.storageKey,
		Size:       spec.stored.size,
		SHA256:     spec.stored.sha256hex,
		Filename:   spec.filename,
		ExeFormat:  string(spec.stored.format),
	}
	if err := h.DB.CreateMultiPlatformArtifact(ctx, artifact, spec.platforms); err != nil {
		spec.span.RecordError(err)
		if errors.Is(err, db.ErrConflict) {
			jsonError(w, http.StatusConflict, err.Error())
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to record artifact")
		return
	}

	jsonResponse(w, http.StatusCreated, db.ArtifactWithPlatforms{Artifact: *artifact, Platforms: spec.platforms})
}

// firstWindows returns the first windows platform in the declared set.
func firstWindows(platforms []db.Platform) (db.Platform, bool) {
	for _, p := range platforms {
		if p.OS == db.OSWindows {
			return p, true
		}
	}
	return db.Platform{}, false
}

// artifactKind reads the artifact kind an upload declares, from the query
// parameter or the header, defaulting to "binary".
func artifactKind(r *http.Request) string {
	if kind := r.URL.Query().Get("kind"); kind != "" {
		return kind
	}
	if kind := r.Header.Get("X-Artifact-Kind"); kind != "" {
		return kind
	}
	return string(db.KindBinary)
}
