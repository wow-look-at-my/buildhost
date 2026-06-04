package sites

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
	ctx, span := sitesTracer.Start(r.Context(), "sites.serve")
	defer span.End()

	project := auth.ProjectFrom(ctx)
	rt := routeFrom(ctx)

	// Redirect a branch root with no trailing slash (e.g. /p/branch/main) to the
	// slashed form so relative links in index.html resolve under the branch, not
	// its parent. This redirect used to live on its own GET /{project}/branch/{branch}
	// route, but that route's {branch} param greedily matched any sub-path and,
	// scoring higher than this {path...} route, shadowed it -- so every file
	// request hit the redirect and looped (/x -> /x/ -> /x/ ...). Folding it in
	// here keeps a single GET route, so file requests reach Serve directly.
	if rt.path == "" && !strings.HasSuffix(r.URL.Path, "/") {
		http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
		return
	}

	// Decide "is this a directory request?" from the original request path, not
	// rt.path: the router strips the trailing slash from the {path...} capture,
	// so a request for /p/branch/main/docs/ arrives here as rt.path="docs". Only
	// r.URL.Path preserves the trailing slash that distinguishes a directory
	// (serve its index.html) from a file. Reading it off rt.path silently broke
	// subdirectory index serving -- the bug the bypassing unit tests masked by
	// hand-feeding route{path:"docs/"} the real router never produces.
	isDir := rt.path == "" || strings.HasSuffix(r.URL.Path, "/")
	filePath := path.Clean(rt.path)
	if isDir || filePath == "." {
		filePath = path.Join(filePath, "index.html")
	}

	span.SetAttributes(
		attribute.String("sites.project", project.Name),
		attribute.String("sites.branch", rt.branch),
		attribute.String("sites.path", filePath),
	)

	site, err := h.DB.GetSite(ctx, project.ID, rt.branch)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	rc, _, err := h.Store.Get(ctx, site.StorageKey)
	if err != nil {
		http.Error(w, "site data not found", http.StatusInternalServerError)
		return
	}
	defer rc.Close()

	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, "corrupt site archive", http.StatusInternalServerError)
			return
		}

		name := path.Clean(hdr.Name)
		if name == filePath {
			ct := contentType(name)
			w.Header().Set("Content-Type", ct)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", hdr.Size))
			w.Header().Set("Cache-Control", "no-cache")
			// A hosted site is a real web page that must load its own
			// scripts/styles/images. Override the server-wide API CSP
			// ("default-src 'none'"), which would otherwise block every asset
			// and render the site blank, with one that permits the site's own
			// same-origin resources and data: URIs -- the same policy the admin
			// server uses to serve this kind of SPA.
			w.Header().Set("Content-Security-Policy", "default-src 'self' data:")
			io.Copy(w, tr)
			return
		}
	}

	http.NotFound(w, r)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project := auth.ProjectFrom(ctx)

	sites, err := h.DB.ListSites(ctx, project.ID)
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	if sites == nil {
		sites = []db.Site{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sites)
}

func contentType(name string) string {
	ext := filepath.Ext(name)
	ct := mime.TypeByExtension(ext)
	if ct != "" {
		return ct
	}
	switch ext {
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".mjs":
		return "application/javascript"
	}
	return "application/octet-stream"
}
