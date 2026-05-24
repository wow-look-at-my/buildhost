package apt

import (
	"context"
	"net/http"
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
		handler.Gen = repackage.NewGenerator(auth.Store(), auth.BaseURL())
	})
	auth.HandleHandler("/apt/", parseRoute, http.StripPrefix("/apt", &handler))
}

type route struct {
	project string
	subPath string
}

func (r route) ProjectName() string      { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

func parseRoute(r *http.Request) auth.RouteInfo {
	// parseRoute sees the original URL (before StripPrefix runs).
	// Strip the "/apt/" prefix, then split into project + subpath.
	path := strings.TrimPrefix(r.URL.Path, "/apt/")
	parts := strings.SplitN(path, "/", 2)
	rt := route{project: parts[0]}
	if len(parts) == 2 {
		rt.subPath = parts[1]
	}
	return rt
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

type Handler struct {
	DB    *db.DB
	Store storage.Storage
	Gen   *repackage.Generator
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
