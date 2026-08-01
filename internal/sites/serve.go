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

	// Branch names may contain "/" (claude/foo). The route's {branch} bound only
	// the FIRST path segment -- with a wildcard following, the router tries the
	// shortest split first and never backtracks on a DB miss -- so a slash-named
	// branch uploaded via the greedy PUT bind used to be unservable (404). Re-split
	// branch/path by longest match against the project's site rows: the same
	// resolution AllowsPublicRead applies, so gate and serve always agree.
	branch, filePath, ok := splitSiteBranch(ctx, h.DB, project.ID, joinPathParts(rt.branch, rt.path))
	if !ok {
		http.NotFound(w, r)
		return
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

// defaultBranch is the branch a project's bare root resolves to: its
// projects.default_branch (learned from GitHub on publish, e.g. "main"),
// falling back to the schema/seed default ("master") when unset. This is the
// same branch the apex "latest" download tracks, so the root site URL and
// "latest" stay consistent.
func defaultBranch(project *db.Project) string {
	if project != nil && project.DefaultBranch != "" {
		return project.DefaultBranch
	}
	return db.LatestBranch
}

// resolveRootBranch returns the branch the bare site root should resolve to. It
// prefers the project's default branch (defaultBranch), but only when a site has
// actually been published there. projects.default_branch is a best-effort hint
// learned from GitHub on publish; it can lag at the seed "master" -- e.g. until a
// GitHub-OIDC publish corrects it, or when buildhost can't reach a private repo
// to learn its real default -- in which case the sites may all live on a branch
// (commonly "main") the hint doesn't name. Blindly trusting it then bounces the
// root to /{project}/branch/{default}/ where no site exists, a guaranteed 404.
//
// So when the default branch has no site, fall back to one that does: prefer the
// conventional "main"/"master" names (so the root lands on the canonical site,
// not a more-recently-updated ephemeral PR-preview branch), then the most
// recently updated site as a last resort. With no DB (unit tests) or no sites at
// all, the default branch is returned unchanged, preserving the prior behavior.
func resolveRootBranch(ctx context.Context, database *db.DB, project *db.Project) string {
	preferred := defaultBranch(project)
	if database == nil || siteExists(ctx, database, project.ID, preferred) {
		return preferred
	}
	for _, b := range [...]string{"main", db.LatestBranch} {
		if b != preferred && siteExists(ctx, database, project.ID, b) {
			return b
		}
	}
	if sites, err := database.ListSites(ctx, project.ID); err == nil && len(sites) > 0 {
		return sites[0].Branch // ListSites is ordered updated_at DESC
	}
	return preferred // no sites at all -- keep the default (Serve 404s as before)
}

func siteExists(ctx context.Context, database *db.DB, projectID int64, branch string) bool {
	if branch == "" {
		return false
	}
	_, err := database.GetSite(ctx, projectID, branch)
	return err == nil
}

// joinPathParts reassembles the raw remainder the router split in two --
// "<branch>[/<path>]" for a branch route, "<project>[/<path>]" for the apex
// one -- so the DB-backed longest-match split can re-split it correctly.
func joinPathParts(head, tail string) string {
	if tail == "" {
		return head
	}
	return head + "/" + tail
}

// splitSiteBranch splits a combined "<branch>[/<path>]" remainder into the
// branch and the file path by LONGEST match against the project's existing
// site rows. Branch names may legally contain "/" (claude/foo), so no purely
// syntactic split can be right: try every segment prefix, longest first, and
// take the first one a site exists for. When sites exist for both "claude" and
// "claude/foo", paths under claude/foo/ resolve to the longer branch -- the
// same shadowing rule as git refs; claude's own files stay reachable at
// claude/<file> for every <file> that is not itself a branch suffix. ok is
// false when no prefix names an existing site.
//
// Candidates that cannot be a stored branch (over 256 chars, or characters
// outside the branch charset -- typically the file-path half of the remainder)
// are skipped, not fatal: "main/caf%C3%A9.js" still resolves to branch "main".
func splitSiteBranch(ctx context.Context, database *db.DB, projectID int64, remainder string) (branch, filePath string, ok bool) {
	segs := strings.Split(remainder, "/")
	for i := len(segs); i >= 1; i-- {
		cand := strings.Join(segs[:i], "/")
		if !validSiteBranch(cand) {
			continue
		}
		if siteExists(ctx, database, projectID, cand) {
			return cand, strings.Join(segs[i:], "/"), true
		}
	}
	return "", "", false
}

// validSiteBranch mirrors the api layer's validGitBranch (and auth's
// validRefName): 1..256 chars of [a-zA-Z0-9._/-]. Enforced on site upload --
// the classic PUT previously stored any bytes the router decoded -- and used
// to skip impossible longest-match candidates on the serve side.
func validSiteBranch(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '/', c == '-':
		default:
			return false
		}
	}
	return true
}

// ServeDefaultBranch serves the apex site path. /{project} (and /{project}/)
// redirects to the canonical /{project}/branch/{default}/ URL as before, and
// /{project}/<file> serves that file from the same resolved default branch --
// so a project's files are reachable under its own root path without the
// caller having to know which branch the site lives on. That is the grammar
// the {project}.<site-domain> scheme already has for a bare path, and it uses
// the same resolveRootBranch chain, so the two schemes address the same file.
func (h *Handler) ServeDefaultBranch(w http.ResponseWriter, r *http.Request) {
	ctx, span := sitesTracer.Start(r.Context(), "sites.serve_default_branch")
	defer span.End()
	r = r.WithContext(ctx)

	if routeFrom(ctx).path == "" {
		h.RedirectToDefaultBranch(w, r)
		return
	}

	setSiteSecurityHeaders(w)
	project := auth.ProjectFrom(ctx)
	h.serveSiteFile(ctx, w, r, project, resolveRootBranch(ctx, h.DB, project), routeFrom(ctx).path)
}

// RedirectToDefaultBranch sends the bare site root (/{project} or /{project}/)
// to /{project}/branch/{default}/, so a project's root URL resolves to its
// canonical site without the caller having to know which branch it lives on.
// The target is a mutable pointer -- the default branch can change and its site
// updates in place -- so it is a 302 marked no-store, never cached like the
// permanent trailing-slash canonicalization in Serve.
func (h *Handler) RedirectToDefaultBranch(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())
	target := "/" + project.Name + "/branch/" + resolveRootBranch(r.Context(), h.DB, project) + "/"
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusFound)
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
