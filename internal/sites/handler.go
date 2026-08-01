package sites

import (
	"context"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var sitesTracer = otel.Tracer("buildhost.sites")

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.DB = auth.DB()
		handler.Store = auth.Store()
		handler.FetchDomains = auth.SiteFetchDomains()
		handler.TmpDir = auth.DataDir() + "/tmp"
		// Config-conditional {project}.<site-domain> scheme (see subdomain.go).
		registerSiteDomainRoutes()
	})
	// The original /branch/{branch}/ form. Kept working forever -- it is what
	// every published preview link, README and deployed client already says --
	// but reads of it 302 to the canonical URL rather than serving in place, so
	// there is one URL per file and exactly one serving implementation.
	auth.ServiceHandle("sites", "PUT /{project}/branch/{branch}", parseRoute, handler.Upload)
	auth.ServiceHandle("sites", "DELETE /{project}/branch/{branch}", parseRoute, handler.Delete)
	auth.ServiceHandle("sites", "GET /{project}/branch/{branch}/{path...}", parseRoute, handler.RedirectLegacyBranch)
	auth.ServiceHandle("sites", "GET /{project}/branches", parseRoute, handler.List)
	// Writes in the canonical "@" form. Two patterns because "@{branch}" is a
	// single path segment and branch names may contain "/" (claude/foo): the
	// second binds the rest, and parseSigilRoute rejoins them. The literal "@"
	// anchors the match, so neither can claim a /branch/ or /branches URL.
	auth.ServiceHandle("sites", "PUT /{project}/@{branch}", parseSigilRoute, handler.Upload)
	auth.ServiceHandle("sites", "PUT /{project}/@{branch}/{rest}", parseSigilRoute, handler.Upload)
	auth.ServiceHandle("sites", "DELETE /{project}/@{branch}", parseSigilRoute, handler.Delete)
	auth.ServiceHandle("sites", "DELETE /{project}/@{branch}/{rest}", parseSigilRoute, handler.Delete)
	// Every remaining GET: the apex site path (/{project} redirects to the
	// default branch, /{project}/<file> serves that file from it) AND reads in
	// the "@" form (/{project}/@{branch}/<file>). {project} has no wildcard
	// after it, so it binds the WHOLE remainder greedily and parseRootRoute
	// splits it -- at the "@" sigil when there is one, else against the DB.
	// The pattern is literal-less, so it scores below the branch/branches routes
	// and only catches paths that aren't one of those -- it never shadows them
	// (router best-match: more literals wins). Reads carry their grammar in the
	// parse func rather than the route table for the same reason the
	// {project}.<site-domain> scheme does: a sigil is not a path segment, so no
	// pattern can express it without out-scoring this route.
	auth.ServiceHandle("sites", "GET /{project}", handler.parseRootRoute, handler.ServeDefaultBranch)
}

// branchSigil introduces the branch in a site URL:
// sites.{domain}/{project}/@{branch}/{path} and, on the project-subdomain
// scheme, {project}.<site-domain>/@{branch}/{path}. It is outside both the
// branch charset (validSiteBranch) and the project-name charset, so it can
// never be part of a name it separates, and it is vanishingly rare in real
// file names -- which is what makes it safe to reserve at that position.
const branchSigil = '@'

// legacyBranchSigil is the {project}.<site-domain> scheme's original branch
// sigil. Still accepted, and 301'd to the "@" form, so no published URL breaks.
const legacyBranchSigil = '~'

type route struct {
	project string
	branch  string
	path    string
	write   bool
	// root marks a default-branch read: the apex /{project}[/<file>] path on
	// the classic scheme, and a bare (no sigil) path on the
	// {project}.<site-domain> scheme. It distinguishes those from the
	// /{project}/branches listing, which also carries an empty branch. Both
	// gate and serve resolve the actual branch via resolveRootBranch.
	root bool
	// sigil is set by a read that named its branch with the "@" sigil, on either
	// scheme: everything after it, i.e. "<branch>[/<path>]". The split is
	// resolved by longest match against existing site rows (splitSiteBranch),
	// because branch names may contain "/". Never set together with branch.
	sigil string
}

// ref is the raw "<branch>[/<path>]" a branch read carries, whichever form
// named the branch: the "@" sigil, or the classic /branch/{branch}/{path...}
// route, whose {branch} bound only the FIRST segment of a slash-named branch.
// Both are resolved the same way, so the two spellings of a URL always address
// the same file.
func (r route) ref() string {
	if r.sigil != "" {
		return r.sigil
	}
	return joinPathParts(r.branch, r.path)
}

func (r route) ProjectName() string { return r.project }
func (r route) Access() auth.AccessLevel {
	if r.write {
		return auth.WriteAccess
	}
	return auth.ReadAccess
}

// AllowsPublicRead lets requireProject serve a public site branch without a
// token even when the project is private. A single-branch read (Serve on either
// scheme) and the default-branch reads -- the apex root redirect, an apex file
// path, a bare subdomain path, all of which target resolveRootBranch --
// qualify when the branch in question is public; the /branches listing (the
// only read naming neither a branch nor a root) stays gated, as do writes. This keeps a public site's
// shareable URL working under a private project, mirroring the per-branch
// Serve rule. Every case resolves the branch through the SAME helper its
// serving handler uses, so the gate and the serve can never disagree about
// which site a URL addresses.
func (r route) AllowsPublicRead(ctx context.Context, database *db.DB, project *db.Project) bool {
	if r.write {
		return false
	}
	branch := r.branch
	switch {
	case r.root:
		// Resolve the same branch the root redirect / bare subdomain path
		// targets, so the public-read gate and the serve agree.
		branch = resolveRootBranch(ctx, database, project)
	case r.sigil != "" || branch != "":
		// A named branch, in either spelling: the same longest-match
		// resolution Serve and ServeSubdomain apply to route.ref().
		branch, _, _ = splitSiteBranch(ctx, database, project.ID, r.ref())
	}
	if branch == "" {
		return false
	}
	site, err := database.GetSite(ctx, project.ID, branch)
	if err != nil {
		return false
	}
	return site.IsPublic
}

func parseRoute(r *http.Request) auth.RouteInfo {
	return route{
		project: r.PathValue("project"),
		branch:  r.PathValue("branch"),
		path:    r.PathValue("path"),
		write:   r.Method == "PUT" || r.Method == "DELETE",
	}
}

// parseRootRoute parses every sites GET that is not a /branch/ or /branches
// URL. {project} arrives holding the WHOLE remainder (no wildcard follows it,
// so it binds greedily), and this splits it into the two grammars that share
// the route:
//
//	/{project}/@{branch}[/<file>]  an explicit branch, split at the sigil
//	/{project}[/<file>]            the apex path: the project's default branch
//
// The apex form's project/path split is ambiguous by construction -- project
// names are slash-namespaced and file paths have slashes too -- so it is
// resolved by LONGEST match against existing projects (splitProjectPath), the
// same shadowing rule splitSiteBranch applies to slash-named branches. A path
// that names a project outright therefore stays that project's root, exactly as
// before this route served files. The "@" form needs no such lookup: the sigil
// marks exactly where the project name ends.
//
// Resolution only ROUTES; it never grants. requireProject applies its normal
// auth to whatever this resolves to, and every candidate is a prefix of the
// requested path on a route that already answers 401 for a private project, so
// no name becomes discoverable that was not already.
func (h *Handler) parseRootRoute(r *http.Request) auth.RouteInfo {
	remainder := joinPathParts(r.PathValue("project"), r.PathValue("path"))
	if project, ref, ok := splitBranchSigil(remainder); ok {
		return route{project: project, sigil: ref}
	}
	project, filePath := h.splitProjectPath(r.Context(), remainder)
	return route{project: project, path: filePath, root: true}
}

// parseSigilRoute parses a write in the "@" form,
// "/{project}/@{branch}[/<rest of a slash-named branch>]". The router binds
// "@{branch}" to one segment, so a branch like claude/foo arrives split in two;
// rejoin it, because a write names a branch outright (there is no file path on
// this route to be ambiguous with).
func parseSigilRoute(r *http.Request) auth.RouteInfo {
	return route{
		project: r.PathValue("project"),
		branch:  joinPathParts(r.PathValue("branch"), r.PathValue("rest")),
		write:   r.Method == "PUT" || r.Method == "DELETE",
	}
}

// splitBranchSigil splits "<project>/@<branch>[/<path>]" at the first path
// segment introduced by the branch sigil. Everything before it is the project
// name -- exactly, with no DB lookup, however deeply namespaced, because "@"
// cannot occur in a project name -- and everything from the sigil on is the
// "<branch>[/<path>]" remainder splitSiteBranch resolves. ok is false when no
// segment carries the sigil (the apex grammar), when the sigil leads the path
// (no project), or when it names no branch (a bare "@").
func splitBranchSigil(remainder string) (project, ref string, ok bool) {
	segs := strings.Split(remainder, "/")
	for i, s := range segs {
		if s == "" || s[0] != branchSigil {
			continue
		}
		if i == 0 || len(s) == 1 {
			return "", "", false
		}
		return strings.Join(segs[:i], "/"), strings.Join(append([]string{s[1:]}, segs[i+1:]...), "/"), true
	}
	return "", "", false
}

// splitProjectPath splits an apex site path "<project>[/<file path>]" into the
// project name and the file path within its default-branch site, by longest
// match against existing projects: try every segment prefix, longest first,
// and take the first one that names a real project. So "org/repo" stays
// org/repo's root even when a project "org" also exists.
//
// When no prefix matches (or there is no DB), the whole remainder is returned
// as the project name with an empty path, so requireProject answers exactly
// the 404 the bare-root route answered before file paths were served here.
func (h *Handler) splitProjectPath(ctx context.Context, remainder string) (project, filePath string) {
	if h.DB == nil {
		return remainder, ""
	}
	segs := strings.Split(remainder, "/")
	for i := len(segs); i >= 1; i-- {
		cand := strings.Join(segs[:i], "/")
		if _, err := h.DB.GetProject(ctx, cand); err == nil {
			return cand, strings.Join(segs[i:], "/")
		}
	}
	return remainder, ""
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

type Handler struct {
	DB           *db.DB
	Store        storage.Storage
	FetchDomains []string
	TmpDir       string
}
