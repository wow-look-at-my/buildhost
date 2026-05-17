package api

import (
	"errors"
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func (h *Handler) PublishRelease(w http.ResponseWriter, r *http.Request) {
	t := auth.TokenFrom(r.Context())
	if t == nil || !t.HasScope("write") {
		jsonError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	projectName := r.PathValue("project")
	project, err := h.DB.GetProject(r.Context(), projectName)
	if errors.Is(err, db.ErrNotFound) {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to get project")
		return
	}

	version := r.PathValue("version")
	release, err := h.DB.GetRelease(r.Context(), project.ID, version)
	if errors.Is(err, db.ErrNotFound) {
		jsonError(w, http.StatusNotFound, "release not found")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to get release")
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
