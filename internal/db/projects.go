package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("already exists")

func (d *DB) CreateProject(ctx context.Context, p *Project) error {
	res, err := d.q.InsertProject(ctx, InsertProjectParams{
		Name:          p.Name,
		Description:   p.Description,
		Homepage:      p.Homepage,
		License:       p.License,
		IsPrivate:     p.IsPrivate,
		Versioning:    p.Versioning,
		GithubRepo:    p.GithubRepo,
		GithubOwnerID: p.GithubOwnerID,
		GithubRepoID:  p.GithubRepoID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("project %q: %w", p.Name, ErrConflict)
		}
		return fmt.Errorf("insert project: %w", err)
	}
	id, _ := res.LastInsertId()
	p.ID = id
	return nil
}

func (d *DB) GetProject(ctx context.Context, name string) (*Project, error) {
	row, err := d.q.GetProjectByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	return &row, nil
}

func (d *DB) SetProjectVisibility(ctx context.Context, id int64, isPrivate bool) error {
	return d.q.SetProjectVisibility(ctx, SetProjectVisibilityParams{
		IsPrivate: isPrivate,
		ID:        id,
	})
}

func (d *DB) SetProjectGitHubRepo(ctx context.Context, id int64, repo string) error {
	return d.q.SetProjectGitHubRepo(ctx, SetProjectGitHubRepoParams{
		GithubRepo: repo,
		ID:         id,
	})
}

// SetProjectGitHubIDs pins the numeric GitHub owner/repo IDs behind
// github_repo. GitHub names are reusable (a deleted or renamed repo's name can
func (d *DB) SetProjectGitHubIDs(ctx context.Context, id int64, ownerID, repoID string) error {
	return d.q.SetProjectGitHubIDs(ctx, SetProjectGitHubIDsParams{
		GithubOwnerID: ownerID,
		GithubRepoID:  repoID,
		ID:            id,
	})
}

// SetProjectDefaultBranch records the branch the apex "latest" tracks for a
// project. Publishers supply their repo's real default branch on release-create
func (d *DB) SetProjectDefaultBranch(ctx context.Context, id int64, branch string) error {
	return d.q.SetProjectDefaultBranch(ctx, SetProjectDefaultBranchParams{
		DefaultBranch: branch,
		ID:            id,
	})
}

// SetProjectCreateService flips the packaging-agnostic "runs as a background
// service" project setting, which each download format materializes its own
func (d *DB) SetProjectCreateService(ctx context.Context, id int64, enabled bool) error {
	return d.q.SetProjectCreateService(ctx, SetProjectCreateServiceParams{
		CreateService: enabled,
		ID:            id,
	})
}

func (d *DB) ListProjects(ctx context.Context) ([]Project, error) {
	return d.q.ListAllProjects(ctx)
}

// ResolveProject looks a name up as a project, then as an alias a project
// answered to before a rename. Every backend resolves through here, so an old
// dl, apt, brew or npm URL survives a project's rename. The bool reports
// whether the name was an alias.
func (d *DB) ResolveProject(ctx context.Context, name string) (*Project, bool, error) {
	p, err := d.GetProject(ctx, name)
	if err == nil {
		return p, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}
	row, aliasErr := d.q.GetProjectByAlias(ctx, name)
	if errors.Is(aliasErr, sql.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if aliasErr != nil {
		return nil, false, fmt.Errorf("resolve alias: %w", aliasErr)
	}
	return &row, true, nil
}

// NameAvailable reports whether a name is free as a project name AND as an
// alias. They live in separate tables, so SQLite cannot enforce the combined
// uniqueness and every insert path checks it here.
func (d *DB) NameAvailable(ctx context.Context, name string) (bool, error) {
	if _, err := d.GetProject(ctx, name); err == nil {
		return false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	n, err := d.q.CountProjectAliasByName(ctx, name)
	if err != nil {
		return false, fmt.Errorf("alias probe: %w", err)
	}
	return n == 0, nil
}

// RenameProject moves a project to a new name, keeping the previous name as an
// alias. Caller must have confirmed the new name is available.
func (d *DB) RenameProject(ctx context.Context, id int64, oldName, newName string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin rename: %w", err)
	}
	defer tx.Rollback()
	q := d.q.WithTx(tx)
	if err := q.RenameProject(ctx, RenameProjectParams{Name: newName, ID: id}); err != nil {
		return fmt.Errorf("rename project to %q: %w", newName, err)
	}
	// The old name is free, so any alias row for it is this project's own from
	// an earlier rename. The delete makes the insert idempotent.
	if err := q.DeleteProjectAlias(ctx, oldName); err != nil {
		return fmt.Errorf("clear alias %q: %w", oldName, err)
	}
	if err := q.InsertProjectAlias(ctx, InsertProjectAliasParams{Name: oldName, ProjectID: id}); err != nil {
		return fmt.Errorf("alias %q -> %q: %w", oldName, newName, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit rename: %w", err)
	}
	return nil
}

// ProjectsForRepoID lists every project provisioned from a GitHub repo: its
// root project and each child in the repo's namespace.
func (d *DB) ProjectsForRepoID(ctx context.Context, repoID string) ([]Project, error) {
	if repoID == "" {
		return nil, nil
	}
	return d.q.ListProjectsByGitHubRepoID(ctx, repoID)
}

// ProjectAliases lists the names a project answered to before its renames.
func (d *DB) ProjectAliases(ctx context.Context, id int64) ([]string, error) {
	return d.q.ListProjectAliases(ctx, id)
}

func isUniqueViolation(err error) bool {
	return err != nil && (errors.As(err, new(interface{ Code() string })) || containsUniqueConstraint(err.Error()))
}

func containsUniqueConstraint(s string) bool {
	return len(s) > 0 && (contains(s, "UNIQUE constraint") || contains(s, "unique constraint"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
