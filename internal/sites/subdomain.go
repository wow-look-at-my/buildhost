package sites

import (
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/go-containers/set"
)

// The {project}.<site-domain> serving scheme.
//
// When BUILDHOST_SITE_DOMAIN is configured (e.g. pazer.site), each project
// whose name is a valid single DNS label is also served at
// https://<project>.<site-domain>/:
//
//	https://myapp.pazer.site/            -> default branch (resolveRootBranch),
//	https://myapp.pazer.site/docs/x.css     same chain as the classic root
//	                                        redirect, so gate and serve agree
//	https://myapp.pazer.site/@pr-7/      -> branch pr-7 (the "@" sigil names a
//	https://myapp.pazer.site/@pr-7/x.css    branch; it is outside the branch
//	                                        charset, so no branch can collide)
//
// Branches may contain "/" (claude/foo), so "@claude/foo/x.css" is ambiguous
// syntactically; splitSiteBranch resolves it by longest match against the
// project's site rows. An "@<branch>" that resolves to the SAME branch the bare
// root serves is non-canonical and 302s (no-store) to the bare form -- one
// canonical URL per file, and the default branch stays a mutable pointer.
//
// "~" was this scheme's original sigil, before "@" became the one branch
// spelling shared with sites.{domain}/{project}/@{branch}/. It still resolves
// and 301s to the "@" form, so no published URL breaks.
//
// Reserved on this scheme (still served on sites.{domain}/...): the "@" and "~"
// sigils at the path root, and the literal /__sso path (the cross-domain
// sign-in redemption endpoint, registered by internal/auth inside this host
// family because host-agnostic routes never serve on a claimed host).
//
// v1 serves only names already valid as a single DNS label ([a-z0-9-], max 63,
// no leading/trailing hyphen): names with "/", ".", "_", or over 63 chars 404
// here and stay reachable on the classic scheme. No fold-back mapping.

// siteDomainRegistered keeps the config-conditional registration idempotent
// across repeated auth.Init calls (tests boot several servers per process).
var siteDomainRegistered = set.New[string]()

// registerSiteDomainRoutes registers the {project}.<site-domain> serving route
// iff a site domain is configured. The pattern depends on configuration, so it
// runs from auth.OnSiteDomain: with the real domain at auth.Init, and with
// auth.SiteDomainPlaceholder when routes are enumerated rather than served, so
// `buildhost routes` and the PR route diff still cover it.
func registerSiteDomainRoutes(d string) {
	if d == "" || !siteDomainRegistered.Add(d) {
		return
	}
	auth.SiteDomainHandle(d, "GET /{path...}", parseSubdomainRoute, handler.ServeSubdomain)
}

// parseSubdomainRoute builds the sites route for a {project}.<site-domain>
// request: the project is the host label ({project} binds exactly one label),
// the branch/path come from the sigil grammar. The result is the same route
// struct the classic scheme uses, so AllowsPublicRead and the centralized
// requireProject flow apply verbatim (GET only -- always ReadAccess, never
// auto-provisioning).
func parseSubdomainRoute(r *http.Request) auth.RouteInfo {
	// DNS is case-insensitive; project names are lowercase.
	project := strings.ToLower(r.PathValue("project"))
	if !validSiteLabel(project) {
		// Not servable on this scheme (v1 DNS-label gate). An empty project name
		// makes requireProject answer 404.
		return route{}
	}
	p := r.PathValue("path")
	if len(p) == 0 || (p[0] != branchSigil && p[0] != legacyBranchSigil) {
		// Bare path: the default branch. root=true routes both the gate
		// (AllowsPublicRead) and the handler through resolveRootBranch, the same
		// chain the classic bare-root redirect uses.
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
// label on the {project}.<site-domain> scheme (v1): one segment, [a-z0-9-],
// 1..63 chars, no leading or trailing hyphen (the LDH rule). Names outside
// this stay reachable on sites.{domain}/... only.
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
// redirects between the two forms. File serving itself (tar scan, index.html,
// 404.html) is shared with the classic scheme via serveSiteFile.
func (h *Handler) ServeSubdomain(w http.ResponseWriter, r *http.Request) {
	ctx, span := sitesTracer.Start(r.Context(), "sites.serve_subdomain")
	defer span.End()

	setSiteSecurityHeaders(w)

	project := auth.ProjectFrom(ctx)
	rt := routeFrom(ctx)

	// The original "~" spelling names the same branch as "@", permanently, so
	// canonicalize it: one URL per file, and the rest of this handler only has
	// to reason about one sigil.
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
		// chain as the classic root redirect (and as AllowsPublicRead just did
		// for the gate, so they cannot disagree).
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
		// A commit ref that happens to resolve the default branch is NOT
		// collapsed -- it is the most specific spelling there is.
		// 302 (no-store): the default branch is a mutable pointer, so the mapping
		// from @<branch> form to bare form can change with the next publish.
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

	// Canonicalize a branch root without its trailing slash (@pr-7 -> @pr-7/) so
	// relative links in index.html resolve under the branch -- same rule as the
	// classic scheme's branch root.
	if filePath == "" && !strings.HasSuffix(r.URL.Path, "/") {
		http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
		return
	}

	h.serveSiteFile(ctx, w, r, project, branch, filePath)
}
