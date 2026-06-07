package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// RetentionSettings is the UI-editable retention policy (stored as a single row).
type RetentionSettings struct {
	KeepN        int
	RecencyHours int
}

// GetRetentionSettings returns the current policy, falling back to built-in
// defaults if the row has not been seeded yet (e.g. the CLI running before any
// server start).
func (d *DB) GetRetentionSettings(ctx context.Context) (RetentionSettings, error) {
	row, err := d.q.GetRetentionSettings(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return RetentionSettings{KeepN: 10, RecencyHours: 24}, nil
	}
	if err != nil {
		return RetentionSettings{}, fmt.Errorf("get retention settings: %w", err)
	}
	return RetentionSettings{KeepN: int(row.KeepN), RecencyHours: int(row.RecencyHours)}, nil
}

// SeedRetentionSettings inserts the initial policy row if absent (INSERT OR
// IGNORE), so env-configured defaults populate the DB on first start without
// overwriting later UI edits.
func (d *DB) SeedRetentionSettings(ctx context.Context, keepN, recencyHours int) error {
	return d.q.SeedRetentionSettings(ctx, SeedRetentionSettingsParams{
		KeepN:        int64(keepN),
		RecencyHours: int64(recencyHours),
	})
}

// UpdateRetentionSettings persists a new policy (from the admin dashboard).
func (d *DB) UpdateRetentionSettings(ctx context.Context, keepN, recencyHours int) error {
	return d.q.UpdateRetentionSettings(ctx, UpdateRetentionSettingsParams{
		KeepN:        int64(keepN),
		RecencyHours: int64(recencyHours),
	})
}

// sqliteDatetime formats t to match SQLite's datetime('now') text format
// (UTC, second precision) so a string comparison against a stored DATETIME
// column orders chronologically. Passed through datetime(?) in the query, which
// normalizes it to the same form the columns are stored in.
func sqliteDatetime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}

// BlobRef is a content-addressed storage key paired with its recorded byte size.
type BlobRef struct {
	Key  string
	Size int64
}

// EvictReleases deletes the given releases and all of their child rows -- each
// release's artifacts, their packaged artifacts and download counts, and any oci
// tags pointing at them -- in a single transaction, then determines which of
// their content-addressed blobs are no longer referenced by any surviving row.
//
// When commit is true the deletions are committed and the returned blobs are safe
// for the caller to delete from storage. When commit is false the transaction is
// rolled back -- a dry run that changes nothing -- and the returned blobs are
// exactly what eviction WOULD free. Because all releases are deleted within the
// one transaction before the reference check, a blob shared by several evicted
// releases is correctly reported as freed once and only once.
//
// Child rows are deleted before parents (foreign keys are enforced). oci_blob_links
// is intentionally left untouched: it is project-scoped, not release-scoped, and
// pushed-docker releases are excluded from eviction. candidateCount is the number
// of distinct blobs the releases referenced (freed + still-shared).
func (d *DB) EvictReleases(ctx context.Context, releaseIDs []int64, commit bool) (freed []BlobRef, candidateCount int, err error) {
	if len(releaseIDs) == 0 {
		return nil, 0, nil
	}

	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	q := New(tx)

	candidates := make(map[string]int64) // key -> max recorded size
	for _, id := range releaseIDs {
		rows, err := q.ListReleaseBlobKeys(ctx, id)
		if err != nil {
			return nil, 0, fmt.Errorf("collect blob keys for release %d: %w", id, err)
		}
		for _, row := range rows {
			if row.K == "" {
				continue
			}
			if row.Sz > candidates[row.K] {
				candidates[row.K] = row.Sz
			}
		}
		if err := deleteReleaseRows(ctx, q, id); err != nil {
			return nil, 0, err
		}
	}

	for key, size := range candidates {
		n, err := q.IsBlobReferenced(ctx, key)
		if err != nil {
			return nil, 0, fmt.Errorf("check blob references: %w", err)
		}
		if n == 0 {
			freed = append(freed, BlobRef{Key: key, Size: size})
		}
	}
	sort.Slice(freed, func(i, j int) bool { return freed[i].Key < freed[j].Key })

	if commit {
		if err := tx.Commit(); err != nil {
			return nil, 0, fmt.Errorf("commit: %w", err)
		}
	}
	// When commit is false the deferred Rollback discards every change.

	return freed, len(candidates), nil
}

// deleteReleaseRows removes one release's child rows then the release itself,
// child-first because foreign keys are enforced. Runs on the caller's tx-bound
// *Queries so it participates in the surrounding transaction.
func deleteReleaseRows(ctx context.Context, q *Queries, releaseID int64) error {
	if err := q.DeleteReleasePackagedArtifacts(ctx, releaseID); err != nil {
		return fmt.Errorf("delete packaged artifacts: %w", err)
	}
	if err := q.DeleteReleaseDownloadCounts(ctx, releaseID); err != nil {
		return fmt.Errorf("delete download counts: %w", err)
	}
	if err := q.DeleteReleaseOCITags(ctx, releaseID); err != nil {
		return fmt.Errorf("delete oci tags: %w", err)
	}
	if err := q.DeleteReleaseArtifacts(ctx, releaseID); err != nil {
		return fmt.Errorf("delete artifacts: %w", err)
	}
	if err := q.DeleteReleaseRow(ctx, releaseID); err != nil {
		return fmt.Errorf("delete release: %w", err)
	}
	return nil
}

// IsBlobReferenced reports whether any row in any project still references the
// given storage key (across artifacts raw/stripped/debug, packaged artifacts,
// sites, and oci blob links). The global generalization of BlobBelongsToProject.
func (d *DB) IsBlobReferenced(ctx context.Context, key string) (bool, error) {
	n, err := d.q.IsBlobReferenced(ctx, key)
	return n != 0, err
}

// ListEvictableReleases returns published releases past keep-N on their
// (project, branch) that are also older than recencyCutoff and not pinned by an
// oci tag or a pushed-docker artifact.
func (d *DB) ListEvictableReleases(ctx context.Context, keepN int64, recencyCutoff time.Time) ([]ListEvictableReleasesRow, error) {
	return d.q.ListEvictableReleases(ctx, ListEvictableReleasesParams{
		RecencyCutoff: sqliteDatetime(recencyCutoff),
		KeepN:         keepN,
	})
}

// ListAbandonedReleases returns unpublished (partial/failed upload) releases
// older than the cutoff.
func (d *DB) ListAbandonedReleases(ctx context.Context, cutoff time.Time) ([]ListAbandonedReleasesRow, error) {
	return d.q.ListAbandonedReleases(ctx, sqliteDatetime(cutoff))
}

// SumReclaimableBytes returns an upper bound on the logical bytes keep-N eviction
// would free (it does not subtract blobs shared with surviving releases). For the
// admin dashboard estimate; the gc CLI and sweeper report the exact figure.
func (d *DB) SumReclaimableBytes(ctx context.Context, keepN int64, recencyCutoff time.Time) (int64, error) {
	return d.q.SumReclaimableBytes(ctx, SumReclaimableBytesParams{
		RecencyCutoff: sqliteDatetime(recencyCutoff),
		KeepN:         keepN,
	})
}
