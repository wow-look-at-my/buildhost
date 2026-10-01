package sites

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/binarchive"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

const siteNotFoundPage = "404.html"

func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
	ctx, span := sitesTracer.Start(r.Context(), "sites.serve")
	defer span.End()

	setSiteSecurityHeaders(w)

	project := auth.ProjectFrom(ctx)
	rt := routeFrom(ctx)

	// Branch names may contain "/" (claude/foo), and neither spelling of a branch
	branch, filePath, ok := splitSiteBranch(ctx, h.DB, project.ID, rt.ref())
	if !ok {
		http.NotFound(w, r)
		return
	}

	// Naming the default branch is redundant: the bare project path already
	// serves it, and it is the shorter URL. Collapse to it -- redirects always
	// run toward the simpler form, never away from it.
	if rt.sigil != "" && refNamesBranch(rt.sigil, branch) && branch == resolveRootBranch(ctx, h.DB, project) {
		if target, okc := h.apexURLFor(ctx, project, filePath, r); okc {
			if q := r.URL.RawQuery; q != "" {
				target += "?" + q
			}
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		// The bare URL would address a different project (a namespaced sibling
	}

	// Redirect a branch root with no trailing slash (e.g. /p/branch/main) to the
	if filePath == "" && !strings.HasSuffix(r.URL.Path, "/") {
		http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
		return
	}

	h.serveSiteFile(ctx, w, r, project, branch, filePath)
}

func (h *Handler) serveSiteFile(ctx context.Context, w http.ResponseWriter, r *http.Request, project *db.Project, branch, rawPath string) {
	// The {path...} router value has its trailing slash stripped, so detect a
	isDir := rawPath == "" || strings.HasSuffix(r.URL.Path, "/")
	filePath := path.Clean(rawPath)
	if isDir || filePath == "." {
		filePath = path.Join(filePath, "index.html")
	}

	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("sites.project", project.Name),
		attribute.String("sites.branch", branch),
		attribute.String("sites.path", filePath),
	)

	site, err := h.DB.GetSite(ctx, project.ID, branch)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Indexed path: a site stored as a binpazer archive answers "give me this
	if h.serveFromArchive(ctx, w, site.StorageKey, filePath) {
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
			continue
		}
		name := path.Clean(hdr.Name)
		if name == filePath {
			serveTarFile(w, tr, name, hdr, http.StatusOK)
			return
		}
	}

	rc, _, err = h.Store.Get(ctx, site.StorageKey)
	if err != nil {
		http.Error(w, "site data not found", http.StatusInternalServerError)
		return
	}
	defer rc.Close()

	tr = tar.NewReader(rc)
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
			continue
		}
		name := path.Clean(hdr.Name)
		if name == siteNotFoundPage {
			serveTarFile(w, tr, name, hdr, http.StatusNotFound)
			return
		}
	}

	http.NotFound(w, r)
}

func (h *Handler) ServeDefaultBranch(w http.ResponseWriter, r *http.Request) {
	if routeFrom(r.Context()).sigil != "" {
		h.Serve(w, r)
		return
	}

	ctx, span := sitesTracer.Start(r.Context(), "sites.serve_default_branch")
	defer span.End()
	r = r.WithContext(ctx)

	// Set before the redirect below, not after it: a cross-origin fetch is
	setSiteSecurityHeaders(w)

	rt := routeFrom(ctx)
	// The project root without its trailing slash: canonicalize so relative
	if rt.path == "" && !strings.HasSuffix(r.URL.Path, "/") {
		http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
		return
	}

	project := auth.ProjectFrom(ctx)
	h.serveSiteFile(ctx, w, r, project, resolveRootBranch(ctx, h.DB, project), rt.path)
}

func (h *Handler) serveFromArchive(ctx context.Context, w http.ResponseWriter, storageKey, filePath string) bool {
	rg, ok := h.Store.(storage.RandomGetter)
	if !ok {
		return false
	}
	ra, size, err := rg.OpenReaderAt(ctx, storageKey)
	if err != nil {
		return false // ErrRandomUnsupported for a compressed blob, or missing
	}
	defer ra.Close()

	head := make([]byte, len(binarchive.Magic))
	if _, err := ra.ReadAt(head, 0); err != nil || !binarchive.IsArchive(head) {
		return false // a tar blob from before sites were archived
	}
	a, err := binarchive.Open(ra, size)
	if err != nil {
		slog.Warn("sites: opening archive", "storage_key", storageKey, "err", err)
		return false // fall back to the scan rather than fail the request
	}

	if r, e, err := a.OpenFile(filePath); err == nil {
		serveArchiveFile(w, r, e, http.StatusOK)
		return true
	}
	if r, e, err := a.OpenFile(siteNotFoundPage); err == nil {
		serveArchiveFile(w, r, e, http.StatusNotFound)
		return true
	}
	http.Error(w, "404 page not found", http.StatusNotFound)
	return true
}

func serveArchiveFile(w http.ResponseWriter, r io.Reader, e binarchive.Entry, status int) {
	w.Header().Set("Content-Type", contentType(e.Path))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", e.Size))
	w.Header().Set("Cache-Control", "no-cache")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	io.Copy(w, r)
}

func serveTarFile(w http.ResponseWriter, tr *tar.Reader, name string, hdr *tar.Header, status int) {
	w.Header().Set("Content-Type", contentType(name))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", hdr.Size))
	w.Header().Set("Cache-Control", "no-cache")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	io.Copy(w, tr)
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

// setSiteSecurityHeaders drops app-level hardening headers that block hosted
// site assets, then applies the hosted-site isolation and non-credentialed CORS
// headers.
func setSiteSecurityHeaders(w http.ResponseWriter) {
	w.Header().Del("Content-Security-Policy")
	w.Header().Del("X-Frame-Options")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Embedder-Policy", "credentialless")
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
