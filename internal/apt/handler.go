package apt

import (
	"context"
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
	auth.HandleHandler("/apt/", parseRoute, http.StripPrefix("/apt", &handler))
}

type route struct {
	project string
	subPath string
}

func (r route) ProjectName() string      { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

func parseRoute(r *http.Request) auth.RouteInfo {
	// APT repo path: /apt/{project}/{dists|pool}/...
	// {project} may contain '/' (multi-segment names), so we can't take the
	// first '/'-separated token. Find the LAST occurrence of /dists/ or /pool/
	// -- everything before is the project, everything from that boundary
	// onward is the subpath. LastIndex (not Index) so a project name
	// containing the literal "dists" or "pool" still resolves correctly.
	path := strings.TrimPrefix(r.URL.Path, "/apt/")
	for _, marker := range []string{"/dists/", "/pool/"} {
		if i := strings.LastIndex(path, marker); i > 0 {
			return route{
				project: path[:i],
				// Keep the marker (without the leading '/') in subPath so
				// the handler's prefix matches against "dists/..." / "pool/..."
				// continue to work unchanged.
				subPath: path[i+1:],
			}
		}
	}
	// No marker: treat whole path as project (will 404 at the handler since
	// the subPath switch only matches known prefixes).
	return route{project: path}
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

type Handler struct {
	DB    *db.DB
	Store storage.Storage
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
