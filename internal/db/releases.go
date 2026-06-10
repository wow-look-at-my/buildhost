package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (d *DB) CreateRelease(ctx context.Context, r *Release) error {
	res, err := d.q.InsertRelease(ctx, InsertReleaseParams{
		ProjectID:  r.ProjectID,
		Version:    r.Version,
		VersionNum: r.VersionNum,
		GitBranch:  r.GitBranch,
		GitCommit:  r.GitCommit,
		Notes:      r.Notes,
		OciUser:    r.OciUser,
	})
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
	maxNum, err := d.q.GetMaxVersionNum(ctx, projectID)
	if err != nil {
		return 0, fmt.Errorf("max version_num: %w", err)
	}
	if maxNum == 0 {
		return 1, nil
	}
	return maxNum + 1, nil
}

func (d *DB) GetRelease(ctx context.Context, projectID int64, version string) (*Release, error) {
	row, err := d.q.GetReleaseByProjectAndVersion(ctx, GetReleaseByProjectAndVersionParams{
		ProjectID: projectID,
		Version:   version,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get release: %w", err)
	}
	return &row, nil
}

// GetLatestRelease resolves the apex "latest" release (no version, no explicit
// branch). When the project records a default branch, "latest" tracks the newest
// published release on that branch, so a push to a feature branch cannot hijack
// it. If the default branch is unset (legacy projects, non-GHA publishers) or has
// no published release yet, it falls back to the newest release across all
// branches -- the historical behavior.
func (d *DB) GetLatestRelease(ctx context.Context, projectID int64) (*Release, error) {
	branch, err := d.q.GetProjectDefaultBranch(ctx, projectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get project default branch: %w", err)
	}
	if branch != "" {
		row, err := d.q.GetLatestPublishedReleaseByBranch(ctx, GetLatestPublishedReleaseByBranchParams{
			ProjectID: projectID,
			GitBranch: branch,
		})
		if err == nil {
			return &row, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("get latest release on default branch: %w", err)
		}
		// No published release on the default branch yet -> fall back below.
	}

	row, err := d.q.GetLatestPublishedRelease(ctx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get latest release: %w", err)
	}
	return &row, nil
}

func (d *DB) GetLatestReleaseByBranch(ctx context.Context, projectID int64, branch string) (*Release, error) {
	row, err := d.q.GetLatestPublishedReleaseByBranch(ctx, GetLatestPublishedReleaseByBranchParams{
		ProjectID: projectID,
		GitBranch: branch,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get latest release by branch: %w", err)
	}
	return &row, nil
}

func (d *DB) ListReleases(ctx context.Context, projectID int64) ([]Release, error) {
	return d.q.ListReleasesByProject(ctx, projectID)
}

func (d *DB) PublishRelease(ctx context.Context, releaseID int64) error {
	return d.q.PublishRelease(ctx, releaseID)
}
