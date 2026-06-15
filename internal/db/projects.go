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
		Name:        p.Name,
		Description: p.Description,
		Homepage:    p.Homepage,
		License:     p.License,
		IsPrivate:   p.IsPrivate,
		Versioning:  p.Versioning,
		GithubRepo:  p.GithubRepo,
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
