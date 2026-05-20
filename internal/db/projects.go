package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("already exists")

func (d *DB) CreateProject(ctx context.Context, p *model.Project) error {
	res, err := d.ExecContext(ctx,
		`INSERT INTO projects (name, description, homepage, license, is_private, versioning)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		p.Name, p.Description, p.Homepage, p.License, p.IsPrivate, p.Versioning)
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
	p := &model.Project{}
	err := d.QueryRowContext(ctx,
		`SELECT id, name, description, homepage, license, is_private, versioning, created_at, updated_at
		 FROM projects WHERE name = ?`, name).Scan(
		&p.ID, &p.Name, &p.Description, &p.Homepage, &p.License, &p.IsPrivate, &p.Versioning, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	return p, nil
}

func (d *DB) ListProjects(ctx context.Context) ([]model.Project, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, name, description, homepage, license, is_private, versioning, created_at, updated_at
		 FROM projects ORDER BY name LIMIT 1000`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var projects []model.Project
	for rows.Next() {
		var p model.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Homepage, &p.License, &p.IsPrivate, &p.Versioning, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
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
