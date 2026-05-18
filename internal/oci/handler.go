package oci

import (
	"encoding/json"
	"errors"
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
	path := strings.TrimPrefix(r.URL.Path, "/v2")

	if path == "/" || path == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{})
		return
	}

	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}

	projectName := parts[0]
	action := parts[1]

	switch action {
	case "manifests":
		if len(parts) < 3 {
			http.NotFound(w, r)
			return
		}
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
		h.serveManifest(w, r, project, parts[2])
	case "blobs":
		if len(parts) < 3 {
			http.NotFound(w, r)
			return
		}
		h.serveBlob(w, r, parts[2])
	default:
		http.NotFound(w, r)
	}
}
