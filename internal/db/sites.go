package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

func (d *DB) UpsertSite(ctx context.Context, s *model.Site) (oldStorageKey string, err error) {
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	err = tx.QueryRowContext(ctx,
		`SELECT storage_key FROM sites WHERE project_id = ? AND branch = ?`,
		s.ProjectID, s.Branch).Scan(&oldStorageKey)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("lookup existing site: %w", err)
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO sites (project_id, branch, storage_key, size, sha256, file_count, git_commit, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))
		 ON CONFLICT(project_id, branch) DO UPDATE SET
		   storage_key = excluded.storage_key,
		   size = excluded.size,
		   sha256 = excluded.sha256,
		   file_count = excluded.file_count,
		   git_commit = excluded.git_commit,
		   updated_at = datetime('now')`,
		s.ProjectID, s.Branch, s.StorageKey, s.Size, s.SHA256, s.FileCount, s.GitCommit)
	if err != nil {
		return "", fmt.Errorf("upsert site: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}

	id, _ := res.LastInsertId()
	s.ID = id
	return oldStorageKey, nil
}

func (d *DB) GetSite(ctx context.Context, projectID int64, branch string) (*model.Site, error) {
	s := &model.Site{}
	err := d.QueryRowContext(ctx,
		`SELECT id, project_id, branch, storage_key, size, sha256, file_count, git_commit, created_at, updated_at
		 FROM sites WHERE project_id = ? AND branch = ?`, projectID, branch).Scan(
		&s.ID, &s.ProjectID, &s.Branch, &s.StorageKey, &s.Size, &s.SHA256,
		&s.FileCount, &s.GitCommit, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get site: %w", err)
	}
	return s, nil
}

func (d *DB) ListSites(ctx context.Context, projectID int64) ([]model.Site, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, project_id, branch, storage_key, size, sha256, file_count, git_commit, created_at, updated_at
		 FROM sites WHERE project_id = ? ORDER BY updated_at DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	defer rows.Close()

	var sites []model.Site
	for rows.Next() {
		var s model.Site
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.Branch, &s.StorageKey, &s.Size, &s.SHA256,
			&s.FileCount, &s.GitCommit, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan site: %w", err)
		}
		sites = append(sites, s)
	}
	return sites, rows.Err()
}

func (d *DB) DeleteSite(ctx context.Context, projectID int64, branch string) (storageKey string, err error) {
	err = d.QueryRowContext(ctx,
		`SELECT storage_key FROM sites WHERE project_id = ? AND branch = ?`,
		projectID, branch).Scan(&storageKey)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lookup site for delete: %w", err)
	}

	_, err = d.ExecContext(ctx,
		`DELETE FROM sites WHERE project_id = ? AND branch = ?`,
		projectID, branch)
	if err != nil {
		return "", fmt.Errorf("delete site: %w", err)
	}
	return storageKey, nil
}
