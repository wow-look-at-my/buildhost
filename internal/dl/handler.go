package dl

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/static"
)

var dlTracer = otel.Tracer("buildhost.dl")

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.DB = auth.DB()
		handler.BaseURL = auth.BaseURL()
	})
	auth.Handle("GET /dl/{project}/latest/{os}/{arch}", parseRoute, handler.DownloadLatest)
	auth.Handle("GET /dl/{project}/branch/{branch}/{os}/{arch}", parseRoute, handler.DownloadBranch)
	auth.Handle("GET /dl/{project}/{version}/{os}/{arch}", parseRoute, handler.Download)
}

type route struct {
	project string
	version string
	branch  string
	os      string
	arch    string
}

func (r route) ProjectName() string      { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

func parseRoute(r *http.Request) auth.RouteInfo {
	return route{
		project: r.PathValue("project"),
		version: r.PathValue("version"),
		branch:  r.PathValue("branch"),
		os:      r.PathValue("os"),
		arch:    r.PathValue("arch"),
	}
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

type Handler struct {
	DB      *db.DB
	BaseURL string
}

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

const cacheImmutable = "public, max-age=31536000, immutable"

func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())
	rt := routeFrom(r.Context())

	_, span := dlTracer.Start(r.Context(), "dl.resolve_version")
	var (
		release *model.Release
		err     error
	)
	if rt.version == "latest" {
		w.Header().Set("Cache-Control", "no-cache")
		release, err = h.DB.GetLatestRelease(r.Context(), project.ID)
		span.SetAttributes(attribute.String("dl.resolution", "latest"))
	} else {
		w.Header().Set("Cache-Control", cacheImmutable)
		release, err = h.DB.GetRelease(r.Context(), project.ID, rt.version)
		span.SetAttributes(attribute.String("dl.resolution", "exact"))
	}
	span.End()
	if handleDBErr(w, r, err) {
		return
	}

	h.redirectToStatic(w, r, project, release, rt)
}

func (h *Handler) DownloadLatest(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())
	rt := routeFrom(r.Context())

	release, err := h.DB.GetLatestRelease(r.Context(), project.ID)
	if handleDBErr(w, r, err) {
		return
	}

	h.redirectToStatic(w, r, project, release, rt)
}

func (h *Handler) DownloadBranch(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())
	rt := routeFrom(r.Context())

	release, err := h.DB.GetLatestReleaseByBranch(r.Context(), project.ID, rt.branch)
	if handleDBErr(w, r, err) {
		return
	}

	h.redirectToStatic(w, r, project, release, rt)
}

func (h *Handler) redirectToStatic(w http.ResponseWriter, r *http.Request, project *model.Project, release *model.Release, rt route) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "raw"
	}

	version := strings.TrimPrefix(release.Version, "v")
	if version == "" {
		version = fmt.Sprintf("%d", release.VersionNum)
	}

	p := static.For(project.Name).WithVersion(version).WithOS(model.OS(rt.os)).WithArch(model.Arch(rt.arch)).WithFmt(format)
	if r.URL.Query().Get("debug") == "1" {
		p = p.WithDebug(true)
	}
	static.Redirect(w, r, h.BaseURL, p)
}

