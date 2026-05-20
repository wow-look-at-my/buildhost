package brew

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.DB = auth.DB()
		handler.Store = auth.Store()
	})
	auth.HandleHandler("/brew/", parseRoute, http.StripPrefix("/brew", &handler))
}

type route struct {
	project string
}

func (r route) ProjectName() string     { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

func parseRoute(r *http.Request) auth.RouteInfo {
	path := strings.TrimPrefix(r.URL.Path, "/brew/")
	project := strings.TrimSuffix(path, ".rb")
	return route{project: project}
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

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

	project := auth.ProjectFrom(r.Context())
	projectName := project.Name

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
		if h.tryServeBrew(w, r, a.ID, projectName+".rb") {
			return
		}
	}

	http.NotFound(w, r)
}

func (h *Handler) tryServeBrew(w http.ResponseWriter, r *http.Request, artifactID int64, filename string) bool {
	storageKey, _, _, _, err := h.DB.GetPackagedArtifact(r.Context(), artifactID, "brew")
	if err != nil {
		return false
	}
	rc, _, err := h.Store.Get(r.Context(), storageKey)
	if err != nil {
		return false
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/x-ruby")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filename))
	io.Copy(w, rc)
	return true
}
