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
	"github.com/wow-look-at-my/buildhost/internal/model"
)

func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
	ctx, span := sitesTracer.Start(r.Context(), "sites.serve")
	defer span.End()

	project := auth.ProjectFrom(ctx)
	rt := routeFrom(ctx)
	filePath := rt.path

	span.SetAttributes(
		attribute.String("sites.project", project.Name),
		attribute.String("sites.branch", rt.branch),
		attribute.String("sites.path", filePath),
	)

	if filePath == "" || strings.HasSuffix(filePath, "/") {
		filePath = path.Join(filePath, "index.html")
	}

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
			io.Copy(w, tr)
			return
		}
	}

	http.NotFound(w, r)
}

func (h *Handler) ServeIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rt := routeFrom(ctx)
	rt.path = "index.html"
	r = r.WithContext(auth.WithRouteInfo(ctx, rt))
	h.Serve(w, r)
}

func (h *Handler) ServeRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
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
		sites = []model.Site{}
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
