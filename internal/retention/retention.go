package retention

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// Config controls retention policy. KeepN published releases are kept on each
// (project, git_branch); releases newer than RecencyGuard are never evicted; and
// nothing is deleted unless Enforce is true (report-only by default).
type Config struct {
	KeepN        int
	RecencyGuard time.Duration
	Enforce      bool
}

// Retention is the eviction engine shared by the background sweeper, the gc CLI,
// and the admin estimate.
type Retention struct {
	db    *db.DB
	store storage.Storage
	cfg   Config
	clock func() time.Time
}

func New(database *db.DB, store storage.Storage, cfg Config) *Retention {
	return &Retention{db: database, store: store, cfg: cfg, clock: time.Now}
}

// ReleaseRef identifies a release in a Report.
type ReleaseRef struct {
	ID        int64
	ProjectID int64
	Branch    string
	Version   string
}

// Report describes what a retention pass did (Enforced) or would do.
type Report struct {
	Enforced          bool
	EvictedReleases   []ReleaseRef // past keep-N on their branch
	AbandonedReleases []ReleaseRef // unpublished, older than the recency guard
	BlobsDeleted      int          // blobs freed (enforce) or that would be freed (dry run)
	BlobsRetained     int          // candidate blobs kept because still shared
	ReclaimableBytes  int64        // exact bytes freed / that would be freed
}

// Releases is the total number of releases evicted (or that would be).
func (r Report) Releases() int { return len(r.EvictedReleases) + len(r.AbandonedReleases) }

// Plan computes what eviction would do without changing anything.
func (r *Retention) Plan(ctx context.Context) (Report, error) { return r.run(ctx, false) }

// Run performs eviction, honoring the configured Enforce flag. With Enforce
// false it behaves like Plan (report-only).
func (r *Retention) Run(ctx context.Context) (Report, error) { return r.run(ctx, r.cfg.Enforce) }

func (r *Retention) run(ctx context.Context, enforce bool) (Report, error) {
	rep := Report{Enforced: enforce}
	cutoff := r.clock().Add(-r.cfg.RecencyGuard)

	abandoned, err := r.db.ListAbandonedReleases(ctx, cutoff)
	if err != nil {
		return rep, fmt.Errorf("list abandoned releases: %w", err)
	}
	evictable, err := r.db.ListEvictableReleases(ctx, int64(r.cfg.KeepN), cutoff)
	if err != nil {
		return rep, fmt.Errorf("list evictable releases: %w", err)
	}

	ids := make([]int64, 0, len(abandoned)+len(evictable))
	for _, a := range abandoned {
		rep.AbandonedReleases = append(rep.AbandonedReleases,
			ReleaseRef{ID: a.ID, ProjectID: a.ProjectID, Branch: a.GitBranch, Version: a.Version})
		ids = append(ids, a.ID)
	}
	for _, e := range evictable {
		rep.EvictedReleases = append(rep.EvictedReleases,
			ReleaseRef{ID: e.ID, ProjectID: e.ProjectID, Branch: e.GitBranch, Version: e.Version})
		ids = append(ids, e.ID)
	}

	if len(ids) == 0 {
		return rep, nil
	}

	// EvictReleases deletes the rows in one transaction and reports which blobs
	// became unreferenced. With enforce=false it rolls back (a true dry run) yet
	// still returns the exact set that WOULD be freed.
	freed, candidates, err := r.db.EvictReleases(ctx, ids, enforce)
	if err != nil {
		return rep, fmt.Errorf("evict releases: %w", err)
	}
	rep.BlobsDeleted = len(freed)
	rep.BlobsRetained = candidates - len(freed)

	for _, ref := range freed {
		rep.ReclaimableBytes += ref.Size
		if enforce {
			if err := r.store.Delete(ctx, ref.Key); err != nil {
				// Rows are already committed; a failed blob delete only leaks the
				// blob (recoverable by a later sweep). Log and continue.
				slog.WarnContext(ctx, "retention: failed to delete freed blob", "key", ref.Key, "err", err)
			}
		}
	}

	return rep, nil
}
