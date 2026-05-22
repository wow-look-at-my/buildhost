package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/db/dbgen"
	"github.com/wow-look-at-my/buildhost/internal/model"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("already exists")

func (d *DB) CreateProject(ctx context.Context, p *model.Project) error {
	res, err := d.q.InsertProject(ctx, dbgen.InsertProjectParams{
		Name:        p.Name,
		Description: p.Description,
		Homepage:    p.Homepage,
		License:     p.License,
		IsPrivate:   p.IsPrivate,
		Versioning:  string(p.Versioning),
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

func (d *DB) GetProject(ctx context.Context, name string) (*model.Project, error) {
	row, err := d.q.GetProjectByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	return projectFromRow(row), nil
}

func (d *DB) ListProjects(ctx context.Context) ([]model.Project, error) {
	rows, err := d.q.ListAllProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	projects := make([]model.Project, len(rows))
	for i, row := range rows {
		projects[i] = *projectFromRow(row)
	}
	return projects, nil
}

func projectFromRow(row dbgen.Project) *model.Project {
	return &model.Project{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		Homepage:    row.Homepage,
		License:     row.License,
		IsPrivate:   row.IsPrivate,
		Versioning:  model.Versioning(row.Versioning),
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
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
