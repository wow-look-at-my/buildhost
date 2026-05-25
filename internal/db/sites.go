package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (d *DB) UpsertSite(ctx context.Context, s *Site) (oldStorageKey string, err error) {
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	q := New(tx)

	row, lookupErr := q.GetSiteStorageKey(ctx, GetSiteStorageKeyParams{
		ProjectID: s.ProjectID,
		Branch:    s.Branch,
	})
	if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return "", fmt.Errorf("lookup existing site: %w", lookupErr)
	}
	if lookupErr == nil {
		oldStorageKey = row
	}

	res, err := q.UpsertSite(ctx, UpsertSiteParams{
		ProjectID:  s.ProjectID,
		Branch:     s.Branch,
		StorageKey: s.StorageKey,
		Size:       s.Size,
		SHA256:     s.SHA256,
		FileCount:  int64(s.FileCount),
		GitCommit:  s.GitCommit,
	})
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

func (d *DB) GetSite(ctx context.Context, projectID int64, branch string) (*Site, error) {
	row, err := d.q.GetSiteByProjectAndBranch(ctx, GetSiteByProjectAndBranchParams{
		ProjectID: projectID,
		Branch:    branch,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get site: %w", err)
	}
	return &row, nil
}

func (d *DB) ListSites(ctx context.Context, projectID int64) ([]Site, error) {
	return d.q.ListSitesByProject(ctx, projectID)
}

func (d *DB) DeleteSite(ctx context.Context, projectID int64, branch string) (storageKey string, err error) {
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	q := New(tx)

	row, lookupErr := q.GetSiteStorageKey(ctx, GetSiteStorageKeyParams{
		ProjectID: projectID,
		Branch:    branch,
	})
	if errors.Is(lookupErr, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if lookupErr != nil {
		return "", fmt.Errorf("lookup site for delete: %w", lookupErr)
	}

	err = q.DeleteSiteByProjectAndBranch(ctx, DeleteSiteByProjectAndBranchParams{
		ProjectID: projectID,
		Branch:    branch,
	})
	if err != nil {
		return "", fmt.Errorf("delete site: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return row, nil
}
