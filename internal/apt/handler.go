package apt

import (
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
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}

	projectName := parts[0]
	subpath := parts[1]

	project, err := h.DB.GetProject(r.Context(), projectName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if project.IsPrivate {
		t := auth.TokenFrom(r.Context())
		if t == nil || !t.HasScope("read") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !t.AuthorizedForProject(project.ID) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	switch {
	case subpath == "dists/stable/Release" || subpath == "dists/stable/InRelease":
		h.serveRelease(w, r, projectName)
	case strings.HasPrefix(subpath, "dists/stable/main/binary-"):
		h.servePackages(w, r, projectName, subpath)
	case strings.HasPrefix(subpath, "pool/"):
		h.servePool(w, r, projectName, subpath)
	default:
		http.NotFound(w, r)
	}
}
