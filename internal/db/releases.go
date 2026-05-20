package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

func (d *DB) CreateRelease(ctx context.Context, r *model.Release) error {
	res, err := d.ExecContext(ctx,
		`INSERT INTO releases (project_id, version, version_num, git_branch, git_commit, notes)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		r.ProjectID, r.Version, r.VersionNum, r.GitBranch, r.GitCommit, r.Notes)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("release %s: %w", r.Version, ErrConflict)
		}
		return fmt.Errorf("insert release: %w", err)
	}
	id, _ := res.LastInsertId()
	r.ID = id
	return nil
}

func (d *DB) NextVersionNum(ctx context.Context, projectID int64) (int64, error) {
	var maxNum sql.NullInt64
	err := d.QueryRowContext(ctx,
		"SELECT MAX(version_num) FROM releases WHERE project_id = ?", projectID).Scan(&maxNum)
	if err != nil {
		return 0, fmt.Errorf("max version_num: %w", err)
	}
	if !maxNum.Valid {
		return 1, nil
	}
	return maxNum.Int64 + 1, nil
}

func (d *DB) GetRelease(ctx context.Context, projectID int64, version string) (*model.Release, error) {
	r := &model.Release{}
	err := d.QueryRowContext(ctx,
		`SELECT id, project_id, version, version_num, git_branch, git_commit, notes, published, created_at, published_at
		 FROM releases WHERE project_id = ? AND version = ?`, projectID, version).Scan(
		&r.ID, &r.ProjectID, &r.Version, &r.VersionNum, &r.GitBranch, &r.GitCommit, &r.Notes, &r.Published, &r.CreatedAt, &r.PublishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get release: %w", err)
	}
	return r, nil
}

func (d *DB) GetLatestRelease(ctx context.Context, projectID int64) (*model.Release, error) {
	r := &model.Release{}
	err := d.QueryRowContext(ctx,
		`SELECT id, project_id, version, version_num, git_branch, git_commit, notes, published, created_at, published_at
		 FROM releases WHERE project_id = ? AND published = 1
		 ORDER BY version_num DESC LIMIT 1`, projectID).Scan(
		&r.ID, &r.ProjectID, &r.Version, &r.VersionNum, &r.GitBranch, &r.GitCommit, &r.Notes, &r.Published, &r.CreatedAt, &r.PublishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get latest release: %w", err)
	}
	return r, nil
}

func (d *DB) GetLatestReleaseByBranch(ctx context.Context, projectID int64, branch string) (*model.Release, error) {
	r := &model.Release{}
	err := d.QueryRowContext(ctx,
		`SELECT id, project_id, version, version_num, git_branch, git_commit, notes, published, created_at, published_at
		 FROM releases WHERE project_id = ? AND git_branch = ? AND published = 1
		 ORDER BY version_num DESC LIMIT 1`, projectID, branch).Scan(
		&r.ID, &r.ProjectID, &r.Version, &r.VersionNum, &r.GitBranch, &r.GitCommit, &r.Notes, &r.Published, &r.CreatedAt, &r.PublishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get latest release by branch: %w", err)
	}
	return r, nil
}

func (d *DB) ListReleases(ctx context.Context, projectID int64) ([]model.Release, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, project_id, version, version_num, git_branch, git_commit, notes, published, created_at, published_at
		 FROM releases WHERE project_id = ? ORDER BY version_num DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	defer rows.Close()

	var releases []model.Release
	for rows.Next() {
		var r model.Release
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Version, &r.VersionNum, &r.GitBranch, &r.GitCommit, &r.Notes, &r.Published, &r.CreatedAt, &r.PublishedAt); err != nil {
			return nil, fmt.Errorf("scan release: %w", err)
		}
		releases = append(releases, r)
	}
	return releases, rows.Err()
}

func (d *DB) PublishRelease(ctx context.Context, releaseID int64) error {
	_, err := d.ExecContext(ctx,
		"UPDATE releases SET published = 1, published_at = datetime('now') WHERE id = ?", releaseID)
	return err
}
