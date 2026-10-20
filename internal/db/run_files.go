package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RunKey names one CI run. RepoID and RunID come from the verified OIDC token
// of the job that asks.
type RunKey struct {
	RepoID string
	RunID  string
}

// RunBlob is the stored bytes of one file a run holds.
type RunBlob struct {
	StorageKey string
	Size       int64
}

// TouchRun records a use of the run, and marks it live.
func (d *DB) TouchRun(ctx context.Context, key RunKey, repoPath string) error {
	if err := d.q.TouchRunStorageRun(ctx, TouchRunStorageRunParams{RepoID: key.RepoID, RunID: key.RunID, RepoPath: repoPath}); err != nil {
		return fmt.Errorf("touch run: %w", err)
	}
	return nil
}

// GetRunFile returns the file the run holds under name, and whether it holds one.
func (d *DB) GetRunFile(ctx context.Context, key RunKey, name string) (RunBlob, bool, error) {
	row, err := d.q.GetRunFile(ctx, GetRunFileParams{RepoID: key.RepoID, RunID: key.RunID, Name: name})
	if errors.Is(err, sql.ErrNoRows) {
		return RunBlob{}, false, nil
	}
	if err != nil {
		return RunBlob{}, false, fmt.Errorf("get run file: %w", err)
	}
	return RunBlob{StorageKey: row.StorageKey, Size: row.Size}, true, nil
}

// PutRunFile records file under name for the run. It returns the storage key
// the name held before, or "" when it held none or held the same blob.
func (d *DB) PutRunFile(ctx context.Context, key RunKey, name string, file RunBlob) (string, error) {
	old, found, err := d.GetRunFile(ctx, key, name)
	if err != nil {
		return "", err
	}
	if err := d.q.UpsertRunFile(ctx, UpsertRunFileParams{
		RepoID: key.RepoID, RunID: key.RunID, Name: name, StorageKey: file.StorageKey, Size: file.Size,
	}); err != nil {
		return "", fmt.Errorf("put run file: %w", err)
	}
	if !found || old.StorageKey == file.StorageKey {
		return "", nil
	}
	return old.StorageKey, nil
}

// ListRunFiles returns every file the run holds.
func (d *DB) ListRunFiles(ctx context.Context, key RunKey) ([]ListRunFilesRow, error) {
	rows, err := d.q.ListRunFiles(ctx, ListRunFilesParams{RepoID: key.RepoID, RunID: key.RunID})
	if err != nil {
		return nil, fmt.Errorf("list run files: %w", err)
	}
	return rows, nil
}

// RunsToCheck returns the live runs whose GitHub status was not read since cutoff.
func (d *DB) RunsToCheck(ctx context.Context, cutoff time.Time) ([]ListRunsToCheckRow, error) {
	rows, err := d.q.ListRunsToCheck(ctx, sqliteDatetime(cutoff))
	if err != nil {
		return nil, fmt.Errorf("list runs to check: %w", err)
	}
	return rows, nil
}

// MarkRunChecked records a read of the run's GitHub status. A nil endedAt
// means the run is still live.
func (d *DB) MarkRunChecked(ctx context.Context, key RunKey, endedAt *time.Time) error {
	if endedAt != nil {
		utc := endedAt.UTC()
		endedAt = &utc
	}
	if err := d.q.MarkRunChecked(ctx, MarkRunCheckedParams{RepoID: key.RepoID, RunID: key.RunID, EndedAt: endedAt}); err != nil {
		return fmt.Errorf("mark run checked: %w", err)
	}
	return nil
}

// RunStorageRuns returns every run that holds storage, the ended runs first
// and oldest-ended first.
func (d *DB) RunStorageRuns(ctx context.Context) ([]ListRunStorageRunsRow, error) {
	rows, err := d.q.ListRunStorageRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("list run storage: %w", err)
	}
	return rows, nil
}

// ListRunBlobFiles returns every run file with the run it belongs to, for the
// retention inventory.
func (d *DB) ListRunBlobFiles(ctx context.Context) ([]ListRunBlobFilesRow, error) {
	return d.q.ListRunBlobFiles(ctx)
}

// DeleteRun forgets the run and its files. It returns the storage keys the
// files held, which the caller deletes once nothing else references them.
func (d *DB) DeleteRun(ctx context.Context, key RunKey) ([]string, error) {
	files, err := d.ListRunFiles(ctx, key)
	if err != nil {
		return nil, err
	}
	if err := d.q.DeleteRunFiles(ctx, DeleteRunFilesParams{RepoID: key.RepoID, RunID: key.RunID}); err != nil {
		return nil, fmt.Errorf("delete run files: %w", err)
	}
	if err := d.q.DeleteRunStorageRun(ctx, DeleteRunStorageRunParams{RepoID: key.RepoID, RunID: key.RunID}); err != nil {
		return nil, fmt.Errorf("delete run: %w", err)
	}
	keys := make([]string, 0, len(files))
	for _, f := range files {
		keys = append(keys, f.StorageKey)
	}
	return keys, nil
}
