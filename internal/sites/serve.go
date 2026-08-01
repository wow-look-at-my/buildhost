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
	// URL delimits one: on /branch/{branch}/{path...} the router bound {branch}
	// to only the FIRST segment (with a wildcard following it tries the shortest
	// split first and never backtracks on a DB miss, so a slash-named branch
	// uploaded via the greedy PUT bind used to be unservable), and "@" marks
	// where the branch STARTS, not where it ends. route.ref() hands over the raw
	// remainder either way; re-split it by longest match against the project's
	// site rows -- the same resolution AllowsPublicRead applies, so gate and
	// serve always agree.
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
			// 302 + no-store: the default branch is a mutable pointer, so which
			// branch this URL collapses into can change with the next publish.
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		// The bare URL would address a different project (a namespaced sibling
		// shadows this file path), so there is no simpler URL for this file.
		// Serve it here rather than redirect somewhere that means something else.
	}

	// Redirect a branch root with no trailing slash (e.g. /p/branch/main) to the
	// slashed form so relative links in index.html resolve under the branch, not
	// its parent. This redirect used to live on its own GET /{project}/branch/{branch}
	// route, but that route's {branch} param greedily matched any sub-path and,
	// scoring higher than this {path...} route, shadowed it -- so every file
	// request hit the redirect and looped (/x -> /x/ -> /x/ ...). Folding it in
	// here keeps a single GET route, so file requests reach Serve directly.
	if filePath == "" && !strings.HasSuffix(r.URL.Path, "/") {
		http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
		return
	}

	h.serveSiteFile(ctx, w, r, project, branch, filePath)
}

// serveSiteFile streams one file of a branch's site archive: the tar scan, the
// index.html directory default, the 404.html fallback, and the content
// headers. Shared by the classic sites.{domain} scheme (Serve) and the
// {project}.<site-domain> scheme (ServeSubdomain), which resolve project and
// branch differently but serve identically. rawPath is the file path within
// the site ("" or a trailing-slash request URL means a directory).
func (h *Handler) serveSiteFile(ctx context.Context, w http.ResponseWriter, r *http.Request, project *db.Project, branch, rawPath string) {
	// The {path...} router value has its trailing slash stripped, so detect a
	// directory request from the real request path -- otherwise a nested dir URL
	// like /scratchpads/foo/ is treated as a file, never gets index.html
	// appended, and matches the 0-byte directory entry in the tar below.
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
	// one file" with a seek. served reports whether it handled the request; a
	// blob that is not an archive (every site uploaded before this format) or
	// that cannot be read at an offset falls through to the tar scan below.
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
			continue // never serve a directory entry as a file (0-byte body)
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

// ServeDefaultBranch serves the two grammars parseRootRoute resolves.
// /{project}/@{ref}/<file> names a branch or commit and serves exactly like the
// /branch/ form. /{project}/ and /{project}/<file> are the CANONICAL site URLs:
// they serve straight from the resolved default branch, so a project's files
// are reachable under its own root path without the caller having to know which
// branch the site lives on -- and without a redirect hop. That is the grammar
// the {project}.<site-domain> scheme already has for a bare path, and it uses
// the same resolveRootBranch chain, so the two schemes address the same file.
//
// The bare root used to 302 to the branch URL. It no longer does: the branch
// URL is the longer, more fragile spelling, so pointing the short URL at it was
// backwards. Redirects now run the other way (see Serve).
func (h *Handler) ServeDefaultBranch(w http.ResponseWriter, r *http.Request) {
	if routeFrom(r.Context()).sigil != "" {
		h.Serve(w, r)
		return
	}

	ctx, span := sitesTracer.Start(r.Context(), "sites.serve_default_branch")
	defer span.End()
	r = r.WithContext(ctx)

	rt := routeFrom(ctx)
	// The project root without its trailing slash: canonicalize so relative
	// links in index.html resolve under the project, not the host root. Same
	// permanent rule a branch root follows in Serve.
	if rt.path == "" && !strings.HasSuffix(r.URL.Path, "/") {
		http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
		return
	}

	setSiteSecurityHeaders(w)
	project := auth.ProjectFrom(ctx)
	h.serveSiteFile(ctx, w, r, project, resolveRootBranch(ctx, h.DB, project), rt.path)
}

// serveFromArchive serves one file out of a binpazer-archived site and reports
// whether it handled the request. This is the whole point of storing sites in
// an indexed container: the response costs a directory read plus one block
// decode, instead of scanning the archive from the start -- twice, when the
// request 404s and the not-found page has to be found too.
//
// It answers false only when the indexed path does not apply (legacy tar blob,
// or a blob the backend cannot read at an offset), never when the file is
// merely missing: a real archive answers its own 404.
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
