package retention

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/go-containers/set"
)

// Config controls retention policy. KeepN published releases are kept on each
type Config struct {
	KeepN        int
	RecencyGuard time.Duration
	Enforce      bool
}

// Retention is the eviction engine shared by the background sweeper, the gc CLI,
// and the admin estimate.
type Retention struct {
	db            *db.DB
	store         storage.Storage
	cfg           Config
	clock         func() time.Time
	recordDeleter RecordDeleter
}

func New(database *db.DB, store storage.Storage, cfg Config) *Retention {
	return &Retention{db: database, store: store, cfg: cfg, clock: time.Now}
}

// WithRecordDeleter attaches the sink that marks an org's artifact-metadata
func (r *Retention) WithRecordDeleter(d RecordDeleter) *Retention {
	r.recordDeleter = d
	return r
}

// ConfigFromSettings builds an engine Config from the stored (UI-editable) policy
// plus a runtime enforce decision -- the policy lives in the DB, while whether a
func ConfigFromSettings(s db.RetentionSettings, enforce bool) Config {
	return Config{
		KeepN:        s.KeepN,
		RecencyGuard: time.Duration(s.RecencyHours) * time.Hour,
		Enforce:      enforce,
	}
}

type BlobRef struct {
	Key  string
	Size int64
}

// ReleaseRef identifies a release in a Report.
type ReleaseRef struct {
	ID          int64
	ProjectID   int64
	ProjectName string
	Branch      string
	Version     string
}

// Report describes what a retention pass did (Enforced) or would do.
type Report struct {
	Enforced          bool
	EvictedReleases   []ReleaseRef // past keep-N on their branch
	AbandonedReleases []ReleaseRef // unpublished, older than the recency guard
	BlobsDeleted      int          // blobs freed (enforce) or that would be freed (dry run)
	BlobsRetained     int          // candidate blobs kept because still shared
	ReclaimableBytes  int64        // exact bytes freed / that would be freed
	FreedBlobs        []BlobRef    // the blobs ReclaimableBytes sums, keyed by storage key

	// Artifact-metadata bookkeeping for the evicted releases. An artifact whose
	RecordsMarkedDeleted int // records successfully marked deleted
	RecordsUnmarked      int // records that could NOT be marked (see RecordErrors)
	RecordErrors         []string
}

// Releases is the total number of releases evicted (or that would be).
func (r Report) Releases() int { return len(r.EvictedReleases) + len(r.AbandonedReleases) }

// Plan computes what eviction would do without changing anything.
func (r *Retention) Plan(ctx context.Context) (Report, error) { return r.run(ctx, false) }

// Run performs eviction, honoring the configured Enforce flag. With Enforce
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
			ReleaseRef{ID: a.ID, ProjectID: a.ProjectID, ProjectName: a.ProjectName, Branch: a.GitBranch, Version: a.Version})
		ids = append(ids, a.ID)
	}
	for _, e := range evictable {
		rep.EvictedReleases = append(rep.EvictedReleases,
			ReleaseRef{ID: e.ID, ProjectID: e.ProjectID, ProjectName: e.ProjectName, Branch: e.GitBranch, Version: e.Version})
		ids = append(ids, e.ID)
	}

	if len(ids) == 0 {
		return rep, nil
	}

	// Capture what each doomed release holds BEFORE the rows go: after eviction
	doomed := r.collectRecords(ctx, append(append([]ReleaseRef{}, rep.EvictedReleases...), rep.AbandonedReleases...))

	freed, candidates, err := r.db.EvictReleases(ctx, ids, enforce)
	if err != nil {
		return rep, fmt.Errorf("evict releases: %w", err)
	}
	rep.BlobsDeleted = len(freed)
	rep.BlobsRetained = candidates - len(freed)

	for _, ref := range freed {
		rep.ReclaimableBytes += ref.Size
		rep.FreedBlobs = append(rep.FreedBlobs, BlobRef{Key: ref.Key, Size: ref.Size})
		if enforce {
			if err := r.store.Delete(ctx, ref.Key); err != nil {
				// Rows are already committed; a failed blob delete only leaks the
				slog.WarnContext(ctx, "retention: failed to delete freed blob", "key", ref.Key, "err", err)
			}
		}
	}

	// Only a run that actually deleted has anything to retract. A dry run
	// reports the count it WOULD mark, so an operator sees the work before
	if enforce {
		r.markRecordsDeleted(ctx, &rep, doomed)
	} else {
		rep.RecordsUnmarked = len(doomed)
	}

	return rep, nil
}

type doomedRecord struct {
	githubRepo string
	project    string
	version    string
	sha256     string
}

// collectRecords resolves the artifacts of the releases about to be evicted.
// Releases whose project records no github_repo are skipped: the record was
// posted under some org's linked artifacts page and without the repo there is
// no way to know which, so there is nothing addressable to retract.
func (r *Retention) collectRecords(ctx context.Context, refs []ReleaseRef) []doomedRecord {
	var out []doomedRecord
	repos := make(map[int64]string, len(refs))

	for _, ref := range refs {
		repo, seen := repos[ref.ProjectID]
		if !seen {
			if proj, err := r.db.GetProject(ctx, ref.ProjectName); err == nil && proj != nil {
				repo = proj.GithubRepo
			}
			repos[ref.ProjectID] = repo
		}
		if repo == "" {
			continue
		}
		artifacts, err := r.db.ListArtifacts(ctx, ref.ID)
		if err != nil {
			slog.WarnContext(ctx, "retention: could not list artifacts for eviction bookkeeping",
				"project", ref.ProjectName, "version", ref.Version, "err", err)
			continue
		}
		for _, a := range artifacts {
			if a.SHA256 == "" {
				continue
			}
			out = append(out, doomedRecord{githubRepo: repo, project: ref.ProjectName, version: ref.Version, sha256: a.SHA256})
		}
	}
	return out
}

// markRecordsDeleted retracts the storage records of everything just evicted.
//
// A failure here never rolls back the eviction -- the bytes are already gone,
// and refusing to GC because GitHub is unreachable would be worse. It is
// counted instead: RecordsUnmarked and RecordErrors travel in the Report, the
func (r *Retention) markRecordsDeleted(ctx context.Context, rep *Report, doomed []doomedRecord) {
	if len(doomed) == 0 {
		return
	}
	if r.recordDeleter == nil {
		rep.RecordsUnmarked = len(doomed)
		rep.RecordErrors = append(rep.RecordErrors,
			"no artifact-metadata record deleter configured: the org's linked artifacts page will keep listing these evicted artifacts as stored")
		slog.WarnContext(ctx, "retention: evicted artifacts left recorded as stored",
			"records", len(doomed), "reason", "no record deleter configured")
		return
	}

	seenErr := set.New[string]()
	for _, d := range doomed {
		if err := r.recordDeleter.MarkDeleted(ctx, d.githubRepo, d.project, d.version, d.sha256); err != nil {
			rep.RecordsUnmarked++
			if msg := err.Error(); seenErr.Add(msg) {
				rep.RecordErrors = append(rep.RecordErrors, msg)
			}
			slog.WarnContext(ctx, "retention: could not mark storage record deleted",
				"project", d.project, "version", d.version, "err", err)
			continue
		}
		rep.RecordsMarkedDeleted++
	}
}
