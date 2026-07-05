package dl

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/static"
)

var dlTracer = otel.Tracer("buildhost.dl")

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.DB = auth.DB()
	})
	auth.ServiceHandle("dl", "GET /{project}", parseRoute, handler.Download)
}

type route struct {
	project string
}

func (r route) ProjectName() string      { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

func parseRoute(r *http.Request) auth.RouteInfo {
	return route{project: r.PathValue("project")}
}

type Handler struct {
	DB *db.DB
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

func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())
	q := r.URL.Query()

	osStr := q.Get("os")
	archStr := q.Get("arch")
	if osStr == "" || archStr == "" {
		http.Error(w, "os and arch are required", http.StatusBadRequest)
		return
	}
	// Accept platform-name aliases natively (RUNNER_OS "Linux"/"macOS"/"Windows",
	// RUNNER_ARCH "X64"/"ARM64", uname's "x86_64"/"aarch64", ...) so callers can
	// pass them through verbatim; fold them to the canonical spelling the static
	// endpoint and stored artifacts use. Unrecognized values pass through unchanged.
	if c, ok := db.NormalizeOS(osStr); ok {
		osStr = string(c)
	}
	if c, ok := db.NormalizeArch(archStr); ok {
		archStr = string(c)
	}

	_, span := dlTracer.Start(r.Context(), "dl.resolve_version")
	var (
		release   *db.Release
		err       error
		immutable bool
	)

	version := q.Get("v")
	branch := q.Get("branch")

	switch {
	case version != "":
		release, err = h.DB.GetRelease(r.Context(), project.ID, version)
		span.SetAttributes(attribute.String("dl.resolution", "exact"))
		immutable = true
	case branch != "":
		release, err = h.DB.GetLatestReleaseByBranch(r.Context(), project.ID, branch)
		span.SetAttributes(attribute.String("dl.resolution", "branch"))
	default:
		release, err = h.DB.GetLatestRelease(r.Context(), project.ID)
		span.SetAttributes(attribute.String("dl.resolution", "latest"))
	}
	span.End()

	if handleDBErr(w, r, err) {
		return
	}

	fmtStr := q.Get("fmt")
	if fmtStr == "" {
		fmtStr = "raw"
	}

	resolvedVersion := strings.TrimPrefix(release.Version, "v")
	if resolvedVersion == "" {
		resolvedVersion = fmt.Sprintf("%d", release.VersionNum)
	}

	p := static.For(project.Name).WithVersion(resolvedVersion).WithOS(db.OS(osStr)).WithArch(db.Arch(archStr)).WithFmt(fmtStr)
	if q.Get("debug") == "1" {
		p = p.WithDebug(true)
	}

	code := http.StatusFound
	if immutable {
		// An exact version is an immutable mapping -- safe to cache the redirect
		// itself forever, just like the artifact it points at.
		code = http.StatusMovedPermanently
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// "latest" and branch tips are MUTABLE pointers: a new publish repoints
		// them. Never let a CDN or browser cache this redirect, or clients would
		// stay pinned to a stale release until the cached pointer expires.
		w.Header().Set("Cache-Control", "no-store")
	}
	static.Redirect(w, r, auth.DeriveServiceURL(r, "static"), p, code)
}
