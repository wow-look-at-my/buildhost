package apt

import (
	"errors"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// privatePrefix is the path of the authenticated source.
const privatePrefix = "/private/"

func init() {
	auth.ServiceHandleRaw("apt", "GET /private/dists/{path...}", handler.ServePrivate)
	auth.ServiceHandleRaw("apt", "GET /private/pool/{path...}", handler.ServePrivate)
	auth.ServiceHandleRaw("apt", "GET /private/key.asc", handler.ServePrivate)
}

// ServePrivate serves apt.{domain}/private/, the authenticated source.
func (h *Handler) ServePrivate(w http.ResponseWriter, r *http.Request) {
	t := auth.TokenFrom(r.Context())
	if t == nil || !t.HasScope("read") {
		w.Header().Set("Www-Authenticate", `Basic realm="buildhost"`)
		w.Header().Set("Cache-Control", "private, no-store")
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}

	subpath := strings.TrimPrefix(r.URL.Path, privatePrefix)
	switch {
	case subpath == "key.asc":
		w.Header().Set("Vary", "Authorization")
		h.serveKey(w, r, "private, no-store")
	case strings.HasPrefix(subpath, "pool/"):
		h.servePrivatePool(w, r, strings.TrimPrefix(subpath, "pool/"))
	default:
		idx, err := h.scopeIndex(r)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h.serveDists(w, r, idx, subpath)
	}
}

// scopeIndex lists every project the credential can read, by name, each at
// its latest release. auth.TokenCanReadProject decides, the same rule
// requireProject applies to one project, so the scope never names a project
// the credential cannot open directly. Each project's debs sit in their own
// pool directory, because projects can share a version.
func (h *Handler) scopeIndex(r *http.Request) (aptIndex, error) {
	idx := aptIndex{label: "private", scoped: true, ctx: r.Context(), h: h, baseURL: auth.RequestRootURL(r)}
	projects, err := h.DB.ListProjects(r.Context())
	if err != nil {
		return idx, err
	}
	for i := range projects {
		p := &projects[i]
		if !auth.TokenCanReadProject(r.Context(), p) {
			continue
		}
		release, err := h.DB.GetLatestRelease(r.Context(), p.ID)
		if errors.Is(err, db.ErrNotFound) {
			continue
		}
		if err != nil {
			return idx, err
		}
		idx.entries = append(idx.entries, indexEntry{project: p, release: release, pool: "pool/" + p.Name + "/"})
	}
	return idx, nil
}

// servePrivatePool resolves pool/<project>/<file>.deb inside the scope. A
// private project's redirect carries a signed token, so the credential for
// the apt host is the only one the client needs.
func (h *Handler) servePrivatePool(w http.ResponseWriter, r *http.Request, path string) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Vary", "Authorization")
	i := strings.LastIndex(path, "/")
	if i <= 0 || i == len(path)-1 {
		http.NotFound(w, r)
		return
	}
	project, err := h.DB.GetProject(r.Context(), path[:i])
	if errors.Is(err, db.ErrNotFound) || (err == nil && !auth.TokenCanReadProject(r.Context(), project)) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.poolRedirect(w, r, project, path[i+1:], true)
}
