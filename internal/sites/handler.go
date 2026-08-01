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
	auth.ServiceHandle("sites", "PUT /{project}/branch/{branch}", parseRoute, handler.Upload)
	auth.ServiceHandle("sites", "DELETE /{project}/branch/{branch}", parseRoute, handler.Delete)
	auth.ServiceHandle("sites", "GET /{project}/branch/{branch}/{path...}", parseRoute, handler.Serve)
	auth.ServiceHandle("sites", "GET /{project}/branches", parseRoute, handler.List)
	// The apex site path: /{project} (and /{project}/) redirects to the default
	// branch, and /{project}/<file> serves that file from the same branch.
	// {project} has no wildcard after it, so it binds the WHOLE remainder
	// greedily and parseRootRoute splits project from file path against the DB.
	// The pattern is literal-less, so it scores below the branch/branches routes
	// and only catches paths that aren't one of those -- it never shadows them
	// (router best-match: more literals wins).
	auth.ServiceHandle("sites", "GET /{project}", handler.parseRootRoute, handler.ServeDefaultBranch)
}

type route struct {
	project string
	branch  string
	path    string
	write   bool
	// root marks a default-branch read: the apex /{project}[/<file>] path on
	// the classic scheme, and a bare (no "~") path on the
	// {project}.<site-domain> scheme. It distinguishes those from the
	// /{project}/branches listing, which also carries an empty branch. Both
	// gate and serve resolve the actual branch via resolveRootBranch.
	root bool
	// tilde is set only on the {project}.<site-domain> scheme: everything after
	// the leading "~" sigil, i.e. "<branch>[/<path>]". The split is resolved by
	// longest match against existing site rows (splitSiteBranch), because branch
	// names may contain "/". Never set together with branch.
	tilde string
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
// qualify when the branch in question is public; the /branches listing
// (branch == "" && !root) stays gated, as do writes. This keeps a public site's
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
	case r.tilde != "":
		// {project}.<site-domain>/~<branch>[/<path>]: same longest-match
		// resolution ServeSubdomain applies.
		branch, _, _ = splitSiteBranch(ctx, database, project.ID, r.tilde)
	case branch != "":
		// Classic serve: {branch} may have bound only the first segment of a
		// slash-named branch; same longest-match resolution Serve applies.
		branch, _, _ = splitSiteBranch(ctx, database, project.ID, joinPathParts(r.branch, r.path))
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

// parseRootRoute parses the apex site path "/{project}[/<file>]": the
// project's own root plus any file served from its default branch. branch
// stays empty; the root flag is what distinguishes this from the
// /{project}/branches listing.
//
// The project/path split is ambiguous by construction -- project names are
// slash-namespaced and file paths have slashes too -- so the router cannot make
// it, and {project} arrives holding the whole remainder. Split it here by
// LONGEST match against existing projects (splitProjectPath), the same
// shadowing rule splitSiteBranch applies to slash-named branches. A path that
// names a project outright therefore stays that project's root, exactly as
// before this route served files.
//
// Resolution only ROUTES; it never grants. requireProject applies its normal
// auth to whatever this resolves to, and every candidate is a prefix of the
// requested path on a route that already answers 401 for a private project, so
// no name becomes discoverable that was not already.
func (h *Handler) parseRootRoute(r *http.Request) auth.RouteInfo {
	project, filePath := h.splitProjectPath(r.Context(),
		joinPathParts(r.PathValue("project"), r.PathValue("path")))
	return route{project: project, path: filePath, root: true}
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
