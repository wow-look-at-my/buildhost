package api

import "net/http"

func (h *Handler) PublishRelease(w http.ResponseWriter, r *http.Request) {
	t := h.requireWrite(w, r)
	if t == nil {
		return
	}

	project := h.getProject(w, r, r.PathValue("project"))
	if project == nil {
		return
	}

	if !t.AuthorizedForProject(project.ID) {
		jsonError(w, http.StatusForbidden, "token not authorized for this project")
		return
	}

	release := h.getRelease(w, r, project.ID, r.PathValue("version"))
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
