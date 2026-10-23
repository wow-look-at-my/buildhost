package apt

import _ "embed"

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/go-containers/set"
)

//go:embed install-script-template.sh
var installScriptTemplate string

// serveInstallScript renders the per-project apt installer. Auth (for private
// projects) has already been enforced by requireProject, so reaching here means
// the caller is allowed to read this project.
func (h *Handler) serveInstallScript(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())

	deps, err := h.dependsClosure(r.Context(), project)
	if err != nil {
		slog.Error("apt install.sh depends", "project", project.Name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	base := strings.TrimRight(auth.RequestBaseURL(r), "/")
	var depLines strings.Builder
	for _, dep := range deps {
		private := 0
		if dep.IsPrivate {
			private = 1
		}
		fmt.Fprintf(&depLines, "    add_dep '%s' '%s/%s' '%s' %d\n", dep.Name, base, dep.Name, installSlug(dep.Name), private)
	}

	script := strings.NewReplacer(
		"__PROJECT__", project.Name,
		"__PKG__", repackage.DebPackageName(project.Name),
		"__APT_URL__", base+"/"+project.Name,
		"__PRIVATE_URL__", base+"/private",
		"__SLUG__", installSlug(project.Name),
		"__DEPENDS__", depLines.String(),
	).Replace(installScriptTemplate)

	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(script))
}

// installSlug names the keyring, source list and auth file of a project's repository.
func installSlug(project string) string {
	return "buildhost-" + strings.ReplaceAll(project, "/", "-")
}

// dependsClosure lists every project that project depends on, directly or
// through another dependency, sorted by name. apt resolves a Depends only
// from a configured repository, so install.sh adds the repository of each. A
// stored name that no longer resolves is an error: the install would fail on
// it.
func (h *Handler) dependsClosure(ctx context.Context, project *db.Project) ([]db.Project, error) {
	seen := set.Of[int64](project.ID)
	var deps []db.Project
	queue := project.DependsOn()
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		dep, _, err := h.DB.ResolveProject(ctx, name)
		if errors.Is(err, db.ErrNotFound) {
			return nil, fmt.Errorf("project %s depends on %q, which does not exist", project.Name, name)
		}
		if err != nil {
			return nil, err
		}
		if seen.Contains(dep.ID) {
			continue
		}
		seen.Add(dep.ID)
		deps = append(deps, *dep)
		queue = append(queue, dep.DependsOn()...)
	}
	slices.SortFunc(deps, func(a, b db.Project) int { return strings.Compare(a.Name, b.Name) })
	return deps, nil
}
