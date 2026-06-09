package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
		IsPublic:   s.IsPublic,
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

type DeletedSite struct {
	ProjectID   int64
	ProjectName string
	Branch      string
	StorageKey  string
}

func (d *DB) DeleteSitesByRepositoryBranch(ctx context.Context, repositoryName, branch string) ([]DeletedSite, error) {
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	prefixLike := escapeLike(repositoryName) + "/%"
	rows, err := tx.QueryContext(ctx, `
SELECT sites.project_id, projects.name, sites.branch, sites.storage_key
FROM sites
JOIN projects ON projects.id = sites.project_id
WHERE sites.branch = ?
  AND (projects.name = ? OR projects.name LIKE ? ESCAPE '\')
ORDER BY projects.name`, branch, repositoryName, prefixLike)
	if err != nil {
		return nil, fmt.Errorf("list repository branch sites: %w", err)
	}

	var deleted []DeletedSite
	for rows.Next() {
		var s DeletedSite
		if err := rows.Scan(&s.ProjectID, &s.ProjectName, &s.Branch, &s.StorageKey); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan repository branch site: %w", err)
		}
		deleted = append(deleted, s)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close repository branch sites: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate repository branch sites: %w", err)
	}
	if len(deleted) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit: %w", err)
		}
		return nil, nil
	}

	if _, err := tx.ExecContext(ctx, `
DELETE FROM sites
WHERE branch = ?
  AND project_id IN (
    SELECT id FROM projects
    WHERE name = ? OR name LIKE ? ESCAPE '\'
  )`, branch, repositoryName, prefixLike); err != nil {
		return nil, fmt.Errorf("delete repository branch sites: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return deleted, nil
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
