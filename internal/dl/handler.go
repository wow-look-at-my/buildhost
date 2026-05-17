package dl

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

type Handler struct {
	DB    *db.DB
	Store storage.Storage
}

func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	projectName := r.PathValue("project")
	versionStr := r.PathValue("version")
	osStr := r.PathValue("os")
	archStr := r.PathValue("arch")

	project, err := h.DB.GetProject(r.Context(), projectName)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if project.IsPrivate && !h.authorized(r, project) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var release *model.Release
	if versionStr == "latest" {
		release, err = h.DB.GetLatestRelease(r.Context(), project.ID)
	} else {
		release, err = h.DB.GetRelease(r.Context(), project.ID, versionStr)
	}
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.serveArtifact(w, r, project, release, osStr, archStr)
}

func (h *Handler) DownloadLatest(w http.ResponseWriter, r *http.Request) {
	projectName := r.PathValue("project")
	osStr := r.PathValue("os")
	archStr := r.PathValue("arch")

	project, err := h.DB.GetProject(r.Context(), projectName)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if project.IsPrivate && !h.authorized(r, project) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	release, err := h.DB.GetLatestRelease(r.Context(), project.ID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.serveArtifact(w, r, project, release, osStr, archStr)
}

func (h *Handler) DownloadBranch(w http.ResponseWriter, r *http.Request) {
	projectName := r.PathValue("project")
	branch := r.PathValue("branch")
	osStr := r.PathValue("os")
	archStr := r.PathValue("arch")

	project, err := h.DB.GetProject(r.Context(), projectName)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if project.IsPrivate && !h.authorized(r, project) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	release, err := h.DB.GetLatestReleaseByBranch(r.Context(), project.ID, branch)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.serveArtifact(w, r, project, release, osStr, archStr)
}

func (h *Handler) DownloadDebug(w http.ResponseWriter, r *http.Request) {
	projectName := r.PathValue("project")
	versionStr := r.PathValue("version")
	osStr := r.PathValue("os")
	archStr := r.PathValue("arch")

	project, err := h.DB.GetProject(r.Context(), projectName)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if project.IsPrivate && !h.authorized(r, project) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	release, err := h.DB.GetRelease(r.Context(), project.ID, versionStr)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	artifact, err := h.DB.GetArtifact(r.Context(), release.ID, osStr, archStr)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if artifact.DebugStorageKey == "" {
		http.NotFound(w, r)
		return
	}

	h.serveBlob(w, artifact.DebugStorageKey, fmt.Sprintf("%s-%s.debug", project.Name, versionStr))
}

func (h *Handler) serveArtifact(w http.ResponseWriter, r *http.Request, project *model.Project, release *model.Release, osStr, archStr string) {
	artifact, err := h.DB.GetArtifact(r.Context(), release.ID, osStr, archStr)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" || format == "raw" {
		key := artifact.StorageKey
		if artifact.StrippedStorageKey != "" {
			key = artifact.StrippedStorageKey
		}
		h.serveBlob(w, key, project.Name)
		return
	}

	storageKey, _, _, filename, err := h.DB.GetPackagedArtifact(r.Context(), artifact.ID, format)
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "format not available", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.serveBlob(w, storageKey, filename)
}

func (h *Handler) serveBlob(w http.ResponseWriter, key, filename string) {
	rc, size, err := h.Store.Get(nil, key)
	if err != nil {
		http.Error(w, "blob not found", http.StatusNotFound)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	io.Copy(w, rc)
}

func (h *Handler) authorized(r *http.Request, project *model.Project) bool {
	t := auth.TokenFrom(r.Context())
	if t == nil || !t.HasScope("read") {
		return false
	}
	if t.ProjectID != nil && *t.ProjectID != project.ID {
		return false
	}
	return true
}
