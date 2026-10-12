package sites

import (
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/go-containers/set"
)

// The {project}.<site-domain> serving scheme.

// siteDomainRegistered keeps the config-conditional registration idempotent
var siteDomainRegistered = set.New[string]()

// registerSiteDomainRoutes registers the {project}.<site-domain> serving route
// iff a site domain is configured. The pattern depends on configuration, so it
func registerSiteDomainRoutes(d string) {
	if d == "" || !siteDomainRegistered.Add(d) {
		return
	}
	auth.SiteDomainHandle(d, "GET /{path...}", parseSubdomainRoute, handler.ServeSubdomain)
}

// parseSubdomainRoute builds the sites route for a {project}.<site-domain>
func parseSubdomainRoute(r *http.Request) auth.RouteInfo {
	// DNS is case-insensitive; project names are lowercase.
	project := strings.ToLower(r.PathValue("project"))
	if !validSiteLabel(project) {
		// Not servable on this scheme (v1 DNS-label gate). An empty project name
		return route{}
	}
	p := r.PathValue("path")
	if len(p) == 0 || (p[0] != branchSigil && p[0] != legacyBranchSigil) {
		// Bare path: the default branch. root=true routes both the gate
		return route{project: project, root: true, path: p}
	}
	rest := p[1:]
	if rest == "" || rest[0] == '/' {
		// A sigil with no branch name is not a valid reference.
		return route{}
	}
	return route{project: project, sigil: rest}
}

// validSiteLabel reports whether a project name is servable as a single DNS
func validSiteLabel(s string) bool {
	if len(s) == 0 || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// ServeSubdomain serves a {project}.<site-domain> request: the default branch
// on bare paths, an explicit branch behind the "@" sigil, canonicalizing
func (h *Handler) ServeSubdomain(w http.ResponseWriter, r *http.Request) {
	ctx, span := sitesTracer.Start(r.Context(), "sites.serve_subdomain")
	defer span.End()

	setSiteSecurityHeaders(w)

	project := auth.ProjectFrom(ctx)
	rt := routeFrom(ctx)

	// The original "~" spelling names the same branch as "@", permanently, so
	if strings.HasPrefix(r.URL.Path, "/"+string(legacyBranchSigil)) {
		target := "/" + string(branchSigil) + r.URL.Path[2:]
		if q := r.URL.RawQuery; q != "" {
			target += "?" + q
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}

	if rt.sigil == "" {
		// Bare path: the project's default branch, resolved through the same
		h.serveSiteFile(ctx, w, r, project, resolveRootBranch(ctx, h.DB, project), rt.path)
		return
	}

	branch, filePath, ok := splitSiteBranch(ctx, h.DB, project.ID, rt.sigil)
	if !ok {
		http.NotFound(w, r)
		return
	}

	if refNamesBranch(rt.sigil, branch) && branch == resolveRootBranch(ctx, h.DB, project) {
		// @<default-branch> is non-canonical: the bare path serves this branch.
		target := "/" + filePath
		if filePath != "" && strings.HasSuffix(r.URL.Path, "/") {
			target += "/"
		}
		if q := r.URL.RawQuery; q != "" {
			target += "?" + q
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, target, http.StatusFound)
		return
	}

	if filePath == "" && !strings.HasSuffix(r.URL.Path, "/") {
		http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
		return
	}

	h.serveSiteFile(ctx, w, r, project, branch, filePath)
}
