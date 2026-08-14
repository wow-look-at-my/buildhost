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

	artifacts, err := h.DB.ListArtifactsWithPlatforms(ctx, release.ID)
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

// publishedRelease carries a release plus its artifacts. Publishers assembling
// their own create/upload/publish chain need the digests to record what the
// registry now holds, and this is how they enumerate them: both the publish
// response and GetRelease return this shape.
//
// GetRelease returning it too is what keeps a mid-deploy version skew from
// failing a publish outright. A client that published against an older
// container gets a response without `artifacts`; it can then re-read the
// release until a container that has this field answers, instead of having one
// shot at the publish response. The embedded Release keeps every pre-existing
// field at the top level, so older clients are unaffected either way.
type publishedRelease struct {
	db.Release
	Artifacts []db.ArtifactWithPlatforms `json:"artifacts"`
}
