package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/go-containers/set"
)

type dependsError struct{ msg string }

func (e *dependsError) Error() string { return e.msg }

// resolveDepends turns the declared dependency names of project into canonical project names, sorted.
// A name that does not resolve, or that the caller cannot read, is an unknown project.
// The same answer covers both, so a refusal never confirms that a private project exists.
func (h *Handler) resolveDepends(ctx context.Context, project *db.Project, names []string) ([]string, error) {
	if len(names) > db.MaxDepends {
		return nil, &dependsError{fmt.Sprintf("depends names %d projects, over the limit of %d", len(names), db.MaxDepends)}
	}
	resolved := make([]string, 0, len(names))
	for _, name := range names {
		if !validProjectName(name) {
			return nil, &dependsError{fmt.Sprintf("depends: %q is not a project name", name)}
		}
		dep, _, err := h.DB.ResolveProject(ctx, name)
		if errors.Is(err, db.ErrNotFound) || (err == nil && !auth.TokenCanReadProject(ctx, dep)) {
			return nil, &dependsError{fmt.Sprintf("depends: unknown project %q", name)}
		}
		if err != nil {
			return nil, err
		}
		if dep.ID == project.ID {
			return nil, &dependsError{fmt.Sprintf("depends: project %q cannot depend on itself", project.Name)}
		}
		resolved = append(resolved, dep.Name)
	}
	slices.Sort(resolved)
	resolved = slices.Compact(resolved)
	if err := h.refuseDependsCycle(ctx, project, resolved); err != nil {
		return nil, err
	}
	return resolved, nil
}

// refuseDependsCycle fails when a dependency of project already depends on project, directly or through others.
// Homebrew refuses to install a formula on a dependency cycle.
func (h *Handler) refuseDependsCycle(ctx context.Context, project *db.Project, deps []string) error {
	seen := set.New[string]()
	queue := slices.Clone(deps)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen.Contains(name) {
			continue
		}
		seen.Add(name)
		p, _, err := h.DB.ResolveProject(ctx, name)
		if errors.Is(err, db.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if p.ID == project.ID {
			return &dependsError{fmt.Sprintf("depends: a dependency of %q depends on %q, which makes a cycle", project.Name, project.Name)}
		}
		queue = append(queue, p.DependsOn()...)
	}
	return nil
}

// applyDepends validates and stores a declared depends list. It writes the response and returns false on a refusal.
func (h *Handler) applyDepends(w http.ResponseWriter, r *http.Request, project *db.Project, names *[]string) bool {
	if names == nil {
		return true
	}
	resolved, err := h.resolveDepends(r.Context(), project, *names)
	var refused *dependsError
	if errors.As(err, &refused) {
		jsonError(w, http.StatusBadRequest, refused.msg)
		return false
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to resolve depends")
		return false
	}
	if slices.Equal(resolved, project.DependsOn()) {
		return true
	}
	if err := h.DB.SetProjectDepends(r.Context(), project.ID, resolved); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to update project depends")
		return false
	}
	project.Depends = strings.Join(resolved, ",")
	return true
}
