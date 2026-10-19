package db

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// MaxDepends caps the number of projects one project can depend on.
const MaxDepends = 32

// ParseDepends splits a stored depends value into project names.
func ParseDepends(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// joinDepends sorts and dedupes names, and joins them for storage.
func joinDepends(names []string) string {
	sorted := slices.Clone(names)
	slices.Sort(sorted)
	return strings.Join(slices.Compact(sorted), ",")
}

// DependsOn lists the canonical names of the projects this project depends on.
func (p Project) DependsOn() []string {
	return ParseDepends(p.Depends)
}

// SetProjectDepends stores the names of the projects a project depends on.
// The caller resolves each name to a canonical project name first. An empty list clears it.
func (d *DB) SetProjectDepends(ctx context.Context, id int64, names []string) error {
	if len(names) > MaxDepends {
		return fmt.Errorf("depends names %d projects, over the limit of %d", len(names), MaxDepends)
	}
	for _, n := range names {
		if n == "" || strings.Contains(n, ",") {
			return fmt.Errorf("depends: %q is not a project name", n)
		}
	}
	return d.q.SetProjectDepends(ctx, SetProjectDependsParams{Depends: joinDepends(names), ID: id})
}

// renameDepends rewrites every depends list that names oldName so it names newName.
// A project that is newName itself drops the entry, because a project never depends on itself.
func renameDepends(ctx context.Context, q *Queries, oldName, newName string, newID int64) error {
	rows, err := q.ListProjectsDependingOn(ctx, oldName)
	if err != nil {
		return fmt.Errorf("list dependents of %q: %w", oldName, err)
	}
	for _, row := range rows {
		names := []string{}
		for _, n := range ParseDepends(row.Depends) {
			switch {
			case n != oldName:
				names = append(names, n)
			case row.ID != newID:
				names = append(names, newName)
			}
		}
		if err := q.SetProjectDepends(ctx, SetProjectDependsParams{Depends: joinDepends(names), ID: row.ID}); err != nil {
			return fmt.Errorf("rewrite depends of project %d: %w", row.ID, err)
		}
	}
	return nil
}
