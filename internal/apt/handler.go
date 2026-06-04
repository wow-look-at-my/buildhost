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

		handler.Gen = repackage.NewGenerator(auth.Store(), auth.DB(), auth.DataDir()+"/tmp")
		handler.Signer = NewSigner(auth.DataDir())
	})
	auth.ServiceHandleHandler("apt", "GET /{path...}", parseRoute, &handler)
}

type route struct {
	project string
	subPath string
}

func (r route) ProjectName() string      { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

func parseRoute(r *http.Request) auth.RouteInfo {
	path := strings.TrimPrefix(r.URL.Path, "/")
	for _, marker := range []string{"/dists/", "/pool/"} {
		if i := strings.LastIndex(path, marker); i > 0 {
			return route{
				project: path[:i],
				subPath: path[i+1:],
			}
		}
	}
	for _, suffix := range []string{"/key.asc", "/install.sh"} {
		if strings.HasSuffix(path, suffix) {
			return route{project: strings.TrimSuffix(path, suffix), subPath: suffix[1:]}
		}
	}
	return route{project: path}
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

type Handler struct {
	DB        *db.DB
	Store     storage.Storage

	Gen       *repackage.Generator
	Signer    *Signer
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	subpath := routeFrom(r.Context()).subPath

	switch {
	case subpath == "dists/stable/InRelease":
		h.serveRelease(w, r, true)
	case subpath == "dists/stable/Release":
		h.serveRelease(w, r, false)
	case subpath == "dists/stable/Release.gpg":
		h.serveReleaseGPG(w, r)
	case subpath == "key.asc":
		h.serveKey(w, r)
	case subpath == "install.sh":
		h.serveInstallScript(w, r)
	case strings.HasPrefix(subpath, "dists/stable/main/binary-"):
		h.servePackages(w, r, subpath)
	case strings.HasPrefix(subpath, "pool/"):
		h.servePool(w, r, subpath)
	default:
		http.NotFound(w, r)
	}
}
