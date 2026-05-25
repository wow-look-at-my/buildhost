package apt

import (
	"context"
	"net/http"
	"net/url"
	"strings"

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
		handler.StaticURL = auth.StaticURL()
		handler.Gen = repackage.NewGenerator(auth.Store(), auth.DB(), auth.BaseURL(), auth.DataDir()+"/tmp")

		auth.HandleHandler(auth.ServiceRoute("apt", "/{project}/{subpath...}"), parseRoute, &handler)
	})
}

type route struct {
	project string
	subPath string
}

func (r route) ProjectName() string      { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

func parseRoute(r *http.Request) auth.RouteInfo {
	return route{
		project: r.PathValue("project"),
		subPath: r.PathValue("subpath"),
	}
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

type Handler struct {
	DB        *db.DB
	Store     storage.Storage
	StaticURL *url.URL
	Gen       *repackage.Generator
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	subpath := routeFrom(r.Context()).subPath

	switch {
	case subpath == "dists/stable/Release" || subpath == "dists/stable/InRelease":
		h.serveRelease(w, r)
	case strings.HasPrefix(subpath, "dists/stable/main/binary-"):
		h.servePackages(w, r, subpath)
	case strings.HasPrefix(subpath, "pool/"):
		h.servePool(w, r, subpath)
	default:
		http.NotFound(w, r)
	}
}
