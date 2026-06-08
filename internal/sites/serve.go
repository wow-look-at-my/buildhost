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

	// The {path...} router value has its trailing slash stripped, so detect a
	// directory request from the real request path -- otherwise a nested dir URL
	// like /scratchpads/foo/ is treated as a file, never gets index.html
	// appended, and matches the 0-byte directory entry in the tar below.
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

		if hdr.Typeflag != tar.TypeReg {
			continue // never serve a directory entry as a file (0-byte body)
		}
		name := path.Clean(hdr.Name)
		if name == filePath {
			ct := contentType(name)
			relaxSiteSecurityHeaders(w)
			w.Header().Set("Content-Type", ct)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", hdr.Size))
			w.Header().Set("Cache-Control", "no-cache")
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

// relaxSiteSecurityHeaders drops the app-level hardening headers that the
// global security middleware sets (CSP default-src 'none', X-Frame-Options:
// DENY). Hosted sites are third-party static content on a dedicated subdomain;
// those headers would block a site's own CSS/JS/images and prevent embedding a
// preview, so sites are served without them like any static host. Site files
// also allow cross-origin reads without credentials so preview frames can load
// module assets from the static site origin.
func relaxSiteSecurityHeaders(w http.ResponseWriter) {
	w.Header().Del("Content-Security-Policy")
	w.Header().Del("X-Frame-Options")
	w.Header().Set("Access-Control-Allow-Origin", "*")
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
