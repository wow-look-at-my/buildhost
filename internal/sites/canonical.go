package sites

// Canonicalization: which URL is THE URL for a file, and how every other
// spelling gets there. The rule is one-directional -- a redirect always moves
// toward the shorter URL, never away from it -- so the bare project path is
// canonical, the "@" form is used only when a ref genuinely needs naming, and
// the original /branch/ spelling is a pure redirect shim.

import (
	"context"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// RedirectLegacyBranch answers the original /{project}/branch/{branch}/{path}
// form with a 302 to the canonical URL for the same file: the bare project path
// when that branch is the default one, else the "@" spelling. Redirects run
// toward the shorter URL, and every file ends up with one URL that serves it.
//
// The old form is not going away -- it is what every published preview link,
// README and deployed client already says, and a 302 keeps every one of them
// working. It just stops being a second place that serves bytes.
func (h *Handler) RedirectLegacyBranch(w http.ResponseWriter, r *http.Request) {
	ctx, span := sitesTracer.Start(r.Context(), "sites.redirect_legacy_branch")
	defer span.End()

	// A cross-origin fetch is checked at EVERY hop, so a redirect without the
	// site CORS headers fails the whole load even when its target has them.
	setSiteSecurityHeaders(w)

	project := auth.ProjectFrom(ctx)
	rt := routeFrom(ctx)

	// {branch} bound only the first segment of a slash-named branch, so resolve
	// the real split before naming it in the target -- exactly as Serve does.
	branch, filePath, ok := splitSiteBranch(ctx, h.DB, project.ID, rt.ref())
	if !ok {
		http.NotFound(w, r)
		return
	}

	target, mutable := h.canonicalURLFor(ctx, project, branch, filePath, r)
	if q := r.URL.RawQuery; q != "" {
		target += "?" + q
	}
	if mutable {
		// The target is the bare path, i.e. "whichever branch is default" --
		// which can change with the next publish.
		w.Header().Set("Cache-Control", "no-store")
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// canonicalURLFor returns the canonical URL for a file of a resolved branch:
// the bare project path when that branch is the default and the bare URL really
// addresses this project's file (see apexURLFor), else the "@" spelling.
// mutable reports whether the target depends on which branch is currently
// default, so the caller can mark that response uncacheable.
func (h *Handler) canonicalURLFor(ctx context.Context, project *db.Project, branch, filePath string, r *http.Request) (target string, mutable bool) {
	if branch == resolveRootBranch(ctx, h.DB, project) {
		if bare, ok := h.apexURLFor(ctx, project, filePath, r); ok {
			return bare, true
		}
	}
	target = "/" + project.Name + "/" + string(branchSigil) + branch + "/"
	if filePath != "" {
		target += filePath
		if strings.HasSuffix(r.URL.Path, "/") {
			target += "/"
		}
	}
	return target, false
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
