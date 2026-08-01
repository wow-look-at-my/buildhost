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

// apexURLFor builds the bare project-path URL for a file -- "/{project}/<file>"
// -- and reports whether that URL actually addresses this project's file.
//
// The check is the whole point. The apex path's project/file split is resolved
// by longest match against existing projects, so with projects "org" and
// "org/repo" the URL /org/repo/x.css belongs to org/repo, NOT to the file
// repo/x.css under org. Collapsing org's file to that URL would silently point
// at another project's site, so the caller keeps serving in place instead.
func (h *Handler) apexURLFor(ctx context.Context, project *db.Project, filePath string, r *http.Request) (string, bool) {
	target := "/" + project.Name + "/"
	if filePath != "" {
		target += filePath
		if strings.HasSuffix(r.URL.Path, "/") {
			target += "/"
		}
	}
	gotProject, gotPath := h.splitProjectPath(ctx, joinPathParts(project.Name, filePath))
	if gotProject != project.Name || gotPath != filePath {
		return "", false
	}
	return target, true
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
// root to /{project}/@{default}/ where no site exists, a guaranteed 404.
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

// splitSiteBranch splits a combined "<ref>[/<path>]" remainder into the branch
// serving it and the file path, by LONGEST match against the project's existing
// site rows. Branch names may legally contain "/" (claude/foo), so no purely
// syntactic split can be right: try every segment prefix, longest first, and
// take the first one a site exists for. When sites exist for both "claude" and
// "claude/foo", paths under claude/foo/ resolve to the longer branch -- the
// same shadowing rule as git refs; claude's own files stay reachable at
// claude/<file> for every <file> that is not itself a branch suffix.
//
// Candidates that cannot be a stored branch (over 256 chars, or characters
// outside the branch charset -- typically the file-path half of the remainder)
// are skipped, not fatal: "main/caf%C3%A9.js" still resolves to branch "main".
//
// When no prefix names a branch, the FIRST segment is tried as a git commit
// (resolveCommitRef) -- so @<sha> addresses the build rather than the branch.
// Branches are tried first, so a branch whose name happens to be hex always
// wins, and no URL that resolved before can be repointed by a commit.
// ok is false when neither resolves.
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
	if b, okc := resolveCommitRef(ctx, database, projectID, segs[0]); okc {
		return b, strings.Join(segs[1:], "/"), true
	}
	return "", "", false
}

// refNamesBranch reports whether the ref as SPELLED in the URL is the branch
// name itself, rather than a commit that resolved to it. Only a branch name is
// collapsible into the bare project URL: a commit is the most specific spelling
// there is, and rewriting it to a mutable pointer would throw the pin away.
func refNamesBranch(ref, branch string) bool {
	return ref == branch || strings.HasPrefix(ref, branch+"/")
}

// minCommitRefLen is the shortest abbreviated sha accepted as a commit ref.
// git's own default abbreviation is 7; below that a hex string is far more
// likely to be a file or directory name than a deliberate commit reference.
const minCommitRefLen = 7

// resolveCommitRef resolves a git commit (full sha or an abbreviation of at
// least minCommitRefLen) to the branch whose CURRENT deployment was built from
// it. A commit cannot contain "/", so only one segment is ever a candidate.
//
// Deployments are keyed by branch and replaced in place, so a commit resolves
// only while it is still some branch's live site: the URL serves exactly that
// build or 404s, and never silently becomes a later one. When several branches
// sit on the same commit the newest deployment wins (ListSites/SitesByCommitPrefix
// order by updated_at DESC), so the answer is deterministic.
func resolveCommitRef(ctx context.Context, database *db.DB, projectID int64, seg string) (branch string, ok bool) {
	if database == nil || !looksLikeCommit(seg) {
		return "", false
	}
	sites, err := database.SitesByCommitPrefix(ctx, projectID, strings.ToLower(seg))
	if err != nil || len(sites) == 0 {
		return "", false
	}
	return sites[0].Branch, true
}

// looksLikeCommit reports whether a path segment could be a git commit sha:
// minCommitRefLen..40 hex characters. Anything else never reaches the DB.
func looksLikeCommit(s string) bool {
	if len(s) < minCommitRefLen || len(s) > 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
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
