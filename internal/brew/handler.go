package brew

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.DB = auth.DB()
		handler.Store = auth.Store()
		handler.Gen = repackage.NewGenerator(auth.Store(), auth.DB(), auth.DataDir()+"/tmp")
	})
	auth.ServiceHandle("brew", "GET /{project}", parseRoute, handler.ServeFormula)
}

type route struct {
	project string
}

func (r route) ProjectName() string      { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

func parseRoute(r *http.Request) auth.RouteInfo {
	return route{project: r.PathValue("project")}
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

type Handler struct {
	DB    *db.DB
	Store storage.Storage
	Gen   *repackage.Generator
}

func (h *Handler) ServeFormula(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())

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
		if a.Kind.ServedViaDockerOnly() {
			continue
		}
		out, err := h.Gen.Generate(r.Context(), repackage.FormatBrew, *project, *release, a, auth.RequestBaseURL(r))
		if err != nil {
			continue
		}
		w.Header().Set("Content-Type", "application/x-ruby")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", project.Name+".rb"))
		io.Copy(w, out.Reader)
		return
	}

	http.NotFound(w, r)
}
