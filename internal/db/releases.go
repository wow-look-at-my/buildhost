package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/db/dbgen"
	"github.com/wow-look-at-my/buildhost/internal/model"
)

func (d *DB) CreateRelease(ctx context.Context, r *model.Release) error {
	res, err := d.q.InsertRelease(ctx, dbgen.InsertReleaseParams{
		ProjectID:  r.ProjectID,
		Version:    r.Version,
		VersionNum: r.VersionNum,
		GitBranch:  r.GitBranch,
		GitCommit:  r.GitCommit,
		Notes:      r.Notes,
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

func (d *DB) GetRelease(ctx context.Context, projectID int64, version string) (*model.Release, error) {
	row, err := d.q.GetReleaseByProjectAndVersion(ctx, dbgen.GetReleaseByProjectAndVersionParams{
		ProjectID: projectID,
		Version:   version,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get release: %w", err)
	}
	return releaseFromRow(row), nil
}

func (d *DB) GetLatestRelease(ctx context.Context, projectID int64) (*model.Release, error) {
	row, err := d.q.GetLatestPublishedRelease(ctx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get latest release: %w", err)
	}
	return releaseFromRow(row), nil
}

func (d *DB) GetLatestReleaseByBranch(ctx context.Context, projectID int64, branch string) (*model.Release, error) {
	row, err := d.q.GetLatestPublishedReleaseByBranch(ctx, dbgen.GetLatestPublishedReleaseByBranchParams{
		ProjectID: projectID,
		GitBranch: branch,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get latest release by branch: %w", err)
	}
	return releaseFromRow(row), nil
}

func (d *DB) ListReleases(ctx context.Context, projectID int64) ([]model.Release, error) {
	rows, err := d.q.ListReleasesByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	releases := make([]model.Release, len(rows))
	for i, row := range rows {
		releases[i] = *releaseFromRow(row)
	}
	return releases, nil
}

func (d *DB) PublishRelease(ctx context.Context, releaseID int64) error {
	return d.q.PublishRelease(ctx, releaseID)
}

func releaseFromRow(row dbgen.Release) *model.Release {
	return &model.Release{
		ID:          row.ID,
		ProjectID:   row.ProjectID,
		Version:     row.Version,
		VersionNum:  row.VersionNum,
		GitBranch:   row.GitBranch,
		GitCommit:   row.GitCommit,
		Notes:       row.Notes,
		Published:   row.Published,
		CreatedAt:   row.CreatedAt,
		PublishedAt: row.PublishedAt,
	}
}
