package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RunLockKey names one run lock. RepoID, RunID and RunAttempt come from the
// verified OIDC token of the job that asks.
type RunLockKey struct {
	RepoID     string
	RunID      string
	RunAttempt string
	Name       string
}

// RunLockLifetime is how long a lock is kept.
const RunLockLifetime = 35 * 24 * time.Hour

// GetRunLock returns the value recorded under key, and whether one is.
func (d *DB) GetRunLock(ctx context.Context, key RunLockKey) (string, bool, error) {
	value, err := d.q.GetRunLock(ctx, GetRunLockParams{
		RepoID:     key.RepoID,
		RunID:      key.RunID,
		RunAttempt: key.RunAttempt,
		Name:       key.Name,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get run lock: %w", err)
	}
	return value, true, nil
}

// ClaimRunLock records value under key unless a value is there already. It
// returns the value the table holds afterward, and whether this call wrote it.
// The insert is a single statement, so of racing claims exactly one writes.
func (d *DB) ClaimRunLock(ctx context.Context, key RunLockKey, value string, now time.Time) (string, bool, error) {
	if _, err := d.q.DeleteRunLocksBefore(ctx, sqliteDatetime(now.Add(-RunLockLifetime))); err != nil {
		return "", false, fmt.Errorf("expire run locks: %w", err)
	}
	rows, err := d.q.InsertRunLock(ctx, InsertRunLockParams{
		RepoID:     key.RepoID,
		RunID:      key.RunID,
		RunAttempt: key.RunAttempt,
		Name:       key.Name,
		Value:      value,
	})
	if err != nil {
		return "", false, fmt.Errorf("claim run lock: %w", err)
	}
	held, found, err := d.GetRunLock(ctx, key)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, errors.New("claim run lock: the row is missing after the insert")
	}
	return held, rows == 1, nil
}
