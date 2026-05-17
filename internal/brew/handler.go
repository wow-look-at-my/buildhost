package brew

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

type Handler struct {
	DB    *db.DB
	Store storage.Storage
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if !strings.HasSuffix(path, ".rb") {
		http.NotFound(w, r)
		return
	}

	projectName := strings.TrimSuffix(path, ".rb")
	project, err := h.DB.GetProject(r.Context(), projectName)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if status, ok := auth.EnforceProjectRead(r, project); !ok {
		http.Error(w, http.StatusText(status), status)
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

	artifacts, err := h.DB.ListArtifacts(r.Context(), release.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	for _, a := range artifacts {
		storageKey, _, _, _, err := h.DB.GetPackagedArtifact(r.Context(), a.ID, "brew")
		if err != nil {
			continue
		}
		rc, _, err := h.Store.Get(r.Context(), storageKey)
		if err != nil {
			continue
		}
		defer rc.Close()
		w.Header().Set("Content-Type", "application/x-ruby")
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", projectName+".rb"))
		io.Copy(w, rc)
		return
	}

	http.NotFound(w, r)
}

