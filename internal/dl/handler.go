package dl

import (
	"context"
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

// handleDBErr writes the appropriate HTTP error for a database lookup failure.
// Returns true if an error was written (caller should return).
func handleDBErr(w http.ResponseWriter, r *http.Request, err error) bool {
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return true
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return true
	}
	return false
}

// loadProject fetches a project by name, enforces private-project auth, and writes
// the appropriate error response on failure. Returns nil if the caller should return.
func (h *Handler) loadProject(w http.ResponseWriter, r *http.Request, name string) *model.Project {
	p, err := h.DB.GetProject(r.Context(), name)
	if handleDBErr(w, r, err) {
		return nil
	}
	if p.IsPrivate && !h.authorized(r, p) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil
	}
	return p
}

func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	project := h.loadProject(w, r, r.PathValue("project"))
	if project == nil {
		return
	}

	versionStr := r.PathValue("version")
	var (
		release *model.Release
		err     error
	)
	if versionStr == "latest" {
		release, err = h.DB.GetLatestRelease(r.Context(), project.ID)
	} else {
		release, err = h.DB.GetRelease(r.Context(), project.ID, versionStr)
	}
	if handleDBErr(w, r, err) {
		return
	}

	h.serveArtifact(w, r, project, release, r.PathValue("os"), r.PathValue("arch"))
}

func (h *Handler) DownloadLatest(w http.ResponseWriter, r *http.Request) {
	project := h.loadProject(w, r, r.PathValue("project"))
	if project == nil {
		return
	}

	release, err := h.DB.GetLatestRelease(r.Context(), project.ID)
	if handleDBErr(w, r, err) {
		return
	}

	h.serveArtifact(w, r, project, release, r.PathValue("os"), r.PathValue("arch"))
}

func (h *Handler) DownloadBranch(w http.ResponseWriter, r *http.Request) {
	project := h.loadProject(w, r, r.PathValue("project"))
	if project == nil {
		return
	}

	release, err := h.DB.GetLatestReleaseByBranch(r.Context(), project.ID, r.PathValue("branch"))
	if handleDBErr(w, r, err) {
		return
	}

	h.serveArtifact(w, r, project, release, r.PathValue("os"), r.PathValue("arch"))
}

func (h *Handler) serveArtifact(w http.ResponseWriter, r *http.Request, project *model.Project, release *model.Release, osStr, archStr string) {
	artifact, err := h.DB.GetArtifact(r.Context(), release.ID, osStr, archStr)
	if handleDBErr(w, r, err) {
		return
	}

	if r.URL.Query().Get("debug") == "1" {
		if artifact.DebugStorageKey == "" {
			http.NotFound(w, r)
			return
		}
		h.serveBlob(w, artifact.DebugStorageKey, fmt.Sprintf("%s-%s.debug", project.Name, release.Version))
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
	rc, size, err := h.Store.Get(context.Background(), key)
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
