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
	})
	// Config-conditional {project}.<site-domain> scheme (see subdomain.go).
	auth.OnSiteDomain(registerSiteDomainRoutes)
	// The original /branch/{branch}/ form. Kept working forever -- it is what
	auth.ServiceHandle("sites", "PUT /{project}/branch/{branch}", parseRoute, handler.Upload)
	auth.ServiceHandle("sites", "DELETE /{project}/branch/{branch}", parseRoute, handler.Delete)
	auth.ServiceHandle("sites", "GET /{project}/branch/{branch}/{path...}", parseRoute, handler.RedirectLegacyBranch)
	auth.ServiceHandle("sites", "GET /{project}/branches", parseRoute, handler.List)
	auth.ServiceHandle("sites", "PUT /{project}/@{branch}", parseSigilRoute, handler.Upload)
	auth.ServiceHandle("sites", "PUT /{project}/@{branch}/{rest}", parseSigilRoute, handler.Upload)
	auth.ServiceHandle("sites", "DELETE /{project}/@{branch}", parseSigilRoute, handler.Delete)
	auth.ServiceHandle("sites", "DELETE /{project}/@{branch}/{rest}", parseSigilRoute, handler.Delete)
	// Every remaining GET: the apex site path (/{project} redirects to the
	auth.ServiceHandle("sites", "GET /{project}", handler.parseRootRoute, handler.ServeDefaultBranch)
}

// branchSigil introduces the branch in a site URL:
const branchSigil = '@'

// legacyBranchSigil is the {project}.<site-domain> scheme's original branch
const legacyBranchSigil = '~'

type route struct {
	project string
	branch  string
	path    string
	write   bool
	// root marks a default-branch read: the apex /{project}[/<file>] path on
	root bool
	// sigil is set by a read that named its branch with the "@" sigil, on either
	sigil string
}

// ref is the raw "<branch>[/<path>]" a branch read carries, whichever form
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
func (r route) AllowsPublicRead(ctx context.Context, database *db.DB, project *db.Project) bool {
	if r.write {
		return false
	}
	branch := r.branch
	switch {
	case r.root:
		// Resolve the same branch the root redirect / bare subdomain path
		branch = resolveRootBranch(ctx, database, project)
	case r.sigil != "" || branch != "":
		// A named branch, in either spelling: the same longest-match
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
func parseSigilRoute(r *http.Request) auth.RouteInfo {
	return route{
		project: r.PathValue("project"),
		branch:  joinPathParts(r.PathValue("branch"), r.PathValue("rest")),
		write:   r.Method == "PUT" || r.Method == "DELETE",
	}
}

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
