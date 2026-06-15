package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// LatestBranch is the default value of projects.default_branch (see
// migrations/011_project_default_branch.sql): the branch the apex "latest"
// tracks for a project that has never told buildhost its real default branch.
// It must stay in sync with that migration's column default.
const LatestBranch = "master"

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
// branch): the newest published release on the project's default branch
// (projects.default_branch, default "master"), so a push to a feature branch
// cannot hijack "latest". A project whose default branch has no published
// release yet (the common case right after provisioning) has no apex latest and
// returns ErrNotFound -- there is deliberately no fallback to "newest across all
// branches", which would let a feature-branch build become "latest".
func (d *DB) GetLatestRelease(ctx context.Context, projectID int64) (*Release, error) {
	row, err := d.q.GetLatestPublishedReleaseOnDefaultBranch(ctx, projectID)
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
