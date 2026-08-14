package api

import (
	"errors"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel/attribute"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/exeformat"
)

func init() {
	auth.OnReady(func() {
		auth.HandlePrimary("PUT /api/v1/projects/{project}/releases/{version}/artifacts/ape",
			parseRoute, handler.UploadMultiPlatformArtifact)
	})
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

	// A claim that one file runs on several platforms is only true for a format
	// that actually carries several platforms' code. Reject anything else here
	// rather than publish an unbacked claim -- the badge on the release page
	// reads straight off this.
	if len(platforms) > 1 && !stored.format.MultiPlatformCapable() {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf(
			"upload declares %d platforms but is not an Actually Portable Executable (no MZqFpD magic); "+
				"publish per-platform builds to /artifacts/{os}/{arch} instead", len(platforms)))
		return
	}

	artifact := &db.Artifact{
		ReleaseID:  release.ID,
		Kind:       db.Kind(kind),
		StorageKey: stored.storageKey,
		Size:       stored.size,
		SHA256:     stored.sha256hex,
		Filename:   sanitizeFilename(r.Header.Get("X-Artifact-Filename")),
		ExeFormat:  string(stored.format),
	}
	if err := h.DB.CreateMultiPlatformArtifact(ctx, artifact, platforms); err != nil {
		span.RecordError(err)
		if errors.Is(err, db.ErrConflict) {
			jsonError(w, http.StatusConflict, err.Error())
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to record artifact")
		return
	}

	jsonResponse(w, http.StatusCreated, db.ArtifactWithPlatforms{Artifact: *artifact, Platforms: platforms})
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
