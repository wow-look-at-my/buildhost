package api

import (
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

func init() {
	auth.Handle("POST /api/v1/projects/{project}/releases/{version}/publish",
		parseRoute, handler.PublishRelease)
}

func (h *Handler) PublishRelease(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())
	rt := routeFrom(r.Context())

	release := h.getRelease(w, r, project.ID, rt.version)
	if release == nil {
		return
	}

	if release.Published {
		jsonError(w, http.StatusConflict, "release already published")
		return
	}

	artifacts, err := h.DB.ListArtifacts(r.Context(), release.ID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to list artifacts")
		return
	}
	if len(artifacts) == 0 {
		jsonError(w, http.StatusBadRequest, "no artifacts uploaded")
		return
	}

	if err := h.Orchestrator.PublishRelease(r.Context(), *project, *release); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to publish release")
		return
	}

	release.Published = true
	jsonResponse(w, http.StatusOK, release)
}
