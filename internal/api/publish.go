package api

import (
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func init() {
	auth.OnReady(func() {
		auth.HandlePrimary("POST /api/v1/projects/{project}/releases/{version}/publish",
			parseRoute, handler.PublishRelease)
	})
}

func (h *Handler) PublishRelease(w http.ResponseWriter, r *http.Request) {
	ctx, span := apiTracer.Start(r.Context(), "api.publish_release")
	defer span.End()

	project := auth.ProjectFrom(ctx)
	rt := routeFrom(ctx)

	span.SetAttributes(
		attribute.String("publish.project", project.Name),
		attribute.String("publish.version", rt.version),
	)

	release := h.getRelease(w, r, project.ID, rt.version)
	if release == nil {
		return
	}

	if release.Published {
		jsonError(w, http.StatusConflict, "release already published")
		return
	}

	artifacts, err := h.DB.ListArtifacts(ctx, release.ID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "list artifacts failed")
		jsonError(w, http.StatusInternalServerError, "failed to list artifacts")
		return
	}
	if len(artifacts) == 0 {
		jsonError(w, http.StatusBadRequest, "no artifacts uploaded")
		return
	}

	span.SetAttributes(attribute.Int("publish.artifact_count", len(artifacts)))

	if err := h.Orchestrator.PublishRelease(ctx, *project, *release); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "publish failed")
		jsonError(w, http.StatusInternalServerError, "failed to publish release")
		return
	}

	release.Published = true
	jsonResponse(w, http.StatusOK, publishedRelease{Release: *release, Artifacts: artifacts})
}

// publishedRelease is the publish response: the release, plus exactly what
// became public. Publishers assembling their own create/upload/publish chain
// have no other way to enumerate a release's artifacts (there is no artifacts
// listing endpoint), and they need the digests to record what the registry now
// holds. The embedded Release keeps every pre-existing field at the top level,
// so older clients are unaffected.
type publishedRelease struct {
	db.Release
	Artifacts []db.Artifact `json:"artifacts"`
}
