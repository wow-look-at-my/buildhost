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
