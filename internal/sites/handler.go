package sites

import (
	"context"
	"net/http"

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
	auth.ServiceHandle("sites", "PUT /{project}/branch/{branch}", parseRoute, handler.Upload)
	auth.ServiceHandle("sites", "DELETE /{project}/branch/{branch}", parseRoute, handler.Delete)
	auth.ServiceHandle("sites", "GET /{project}/branch/{branch}/{path...}", parseRoute, handler.Serve)
	auth.ServiceHandle("sites", "GET /{project}/branches", parseRoute, handler.List)
	// Bare site root: /{project} (and /{project}/) redirect to the default
	// branch. {project} is a single param with no literal segments, so it scores
	// below the branch/branches routes and only matches paths that aren't one of
	// those -- it never shadows them (router best-match: more literals wins).
	auth.ServiceHandle("sites", "GET /{project}", parseRootRoute, handler.RedirectToDefaultBranch)
}

type route struct {
	project string
	branch  string
	path    string
	write   bool
	// root marks the bare /{project} root redirect. It distinguishes that route
	// from the /{project}/branches listing, which also carries an empty branch.
	root bool
}

func (r route) ProjectName() string { return r.project }
func (r route) Access() auth.AccessLevel {
	if r.write {
		return auth.WriteAccess
	}
	return auth.ReadAccess
}

// AllowsPublicRead lets requireProject serve a public site branch without a
// token even when the project is private. A single-branch read (Serve) and the
// root redirect (which targets the default branch) both qualify when the branch
// in question is public; the /branches listing (branch == "" && !root) stays
// gated, as do writes. This keeps a public site's shareable root URL working
// under a private project, mirroring the per-branch Serve rule.
func (r route) AllowsPublicRead(ctx context.Context, database *db.DB, project *db.Project) bool {
	if r.write {
		return false
	}
	branch := r.branch
	if r.root {
		branch = defaultBranch(project)
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

// parseRootRoute parses the bare /{project} root. branch and path stay empty;
// the root flag is what distinguishes it from the /{project}/branches listing.
func parseRootRoute(r *http.Request) auth.RouteInfo {
	return route{project: r.PathValue("project"), root: true}
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
