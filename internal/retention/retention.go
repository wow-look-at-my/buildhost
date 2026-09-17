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
	// DeletedBranchAge is the age past which a published release whose branch
	// no longer exists on the origin repository is reclaimed. Zero or negative
	// switches that rule off; keep-N and the abandoned sweep are unaffected.
	DeletedBranchAge time.Duration
}

// Retention is the eviction engine shared by the background sweeper, the gc CLI,
// and the admin estimate.
type Retention struct {
	db            *db.DB
	store         storage.Storage
	cfg           Config
	clock         func() time.Time
	recordDeleter RecordDeleter
	branches      BranchLister
}

func New(database *db.DB, store storage.Storage, cfg Config) *Retention {
	return &Retention{db: database, store: store, cfg: cfg, clock: time.Now}
}

// WithRecordDeleter attaches the sink that marks an org's artifact-metadata
func (r *Retention) WithRecordDeleter(d RecordDeleter) *Retention {
	r.recordDeleter = d
	return r
}

// WithBranchLister attaches the source of truth for branch existence, which the
// deleted-branch rule needs. Without one that rule reclaims nothing and says so
// in the report: an engine that cannot check a branch does not guess at it.
func (r *Retention) WithBranchLister(b BranchLister) *Retention {
	r.branches = b
	return r
}

// ConfigFromSettings builds an engine Config from the stored (UI-editable) policy
// plus a runtime enforce decision -- the policy lives in the DB, while whether a
func ConfigFromSettings(s db.RetentionSettings, enforce bool) Config {
	return Config{
		KeepN:            s.KeepN,
		RecencyGuard:     time.Duration(s.RecencyHours) * time.Hour,
		Enforce:          enforce,
		DeletedBranchAge: time.Duration(s.DeletedBranchDays) * 24 * time.Hour,
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
	Enforced              bool
	EvictedReleases       []ReleaseRef // past keep-N on their branch
	AbandonedReleases     []ReleaseRef // unpublished, older than the recency guard
	DeletedBranchReleases []ReleaseRef // past the age window, branch gone from the remote
	BlobsDeleted          int          // blobs freed (enforce) or that would be freed (dry run)
	BlobsRetained         int          // candidate blobs kept because still shared
	ReclaimableBytes      int64        // exact bytes freed / that would be freed
	FreedBlobs            []BlobRef    // the blobs ReclaimableBytes sums, keyed by storage key

	// The dead-branch reason's own share of the totals above. The reclaim pass
	// evicts those releases in their own refcounted batch, so a blob counted
	// here was freed by this reason alone and is never also counted under
	// keep-N or abandoned.
	DeadBranchBlobs int
	DeadBranchBytes int64

	// Deleted-branch candidates the pass refused to act on. UnknownBranchReleases
	// record no branch at all; BranchLookupsFailed counts the ones whose branch
	// list could not be read, with one message per distinct cause in
	// BranchLookupErrors. Every release counted here was KEPT.
	UnknownBranchReleases []ReleaseRef
	BranchLookupsFailed   int
	BranchLookupErrors    []string

	// Artifact-metadata bookkeeping for the evicted releases. An artifact whose
	RecordsMarkedDeleted int // records successfully marked deleted
	RecordsUnmarked      int // records that could NOT be marked (see RecordErrors)
	RecordErrors         []string
}

// Releases is the total number of releases evicted (or that would be).
func (r Report) Releases() int {
	return len(r.EvictedReleases) + len(r.AbandonedReleases) + len(r.DeletedBranchReleases)
}

// AllEvicted lists every release the pass removes, in one slice.
func (r Report) AllEvicted() []ReleaseRef {
	out := make([]ReleaseRef, 0, r.Releases())
	out = append(out, r.EvictedReleases...)
	out = append(out, r.AbandonedReleases...)
	out = append(out, r.DeletedBranchReleases...)
	return out
}

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

	deletedBranch, err := r.planDeletedBranch(ctx, &rep)
	if err != nil {
		return rep, err
	}

	// One release can qualify under more than one rule. Claiming it once keeps
	// the counts, the listing and the eviction id lists in agreement.
	staleIDs := make([]int64, 0, len(abandoned)+len(evictable))
	deadIDs := make([]int64, 0, len(deletedBranch))
	claimed := set.New[int64](len(abandoned) + len(evictable) + len(deletedBranch))
	claim := func(dst *[]ReleaseRef, ids *[]int64, ref ReleaseRef) {
		if !claimed.Add(ref.ID) {
			return
		}
		*dst = append(*dst, ref)
		*ids = append(*ids, ref.ID)
	}

	for _, a := range abandoned {
		claim(&rep.AbandonedReleases, &staleIDs,
			ReleaseRef{ID: a.ID, ProjectID: a.ProjectID, ProjectName: a.ProjectName, Branch: a.GitBranch, Version: a.Version})
	}
	for _, e := range evictable {
		claim(&rep.EvictedReleases, &staleIDs,
			ReleaseRef{ID: e.ID, ProjectID: e.ProjectID, ProjectName: e.ProjectName, Branch: e.GitBranch, Version: e.Version})
	}
	for _, d := range deletedBranch {
		claim(&rep.DeletedBranchReleases, &deadIDs, d)
	}

	if len(staleIDs)+len(deadIDs) == 0 {
		return rep, nil
	}

	// Capture what each doomed release holds BEFORE the rows go: after eviction
	doomed := r.collectRecords(ctx, rep.AllEvicted())

	freed, candidates, err := r.evictBatch(ctx, enforce, staleIDs)
	if err != nil {
		return rep, err
	}
	rep.BlobsDeleted = len(freed)
	rep.BlobsRetained = candidates - len(freed)
	r.recordFreed(&rep, freed)

	// The dead-branch releases go in their own batch, so their blob and byte
	// totals are the ones this reason actually freed. A blob shared with a
	// surviving release is still retained here, exactly as keep-N retains it.
	if len(deadIDs) > 0 {
		deadFreed, deadCandidates, err := r.evictBatch(ctx, enforce, deadIDs)
		if err != nil {
			return rep, err
		}
		rep.DeadBranchBlobs = len(deadFreed)
		for _, ref := range deadFreed {
			rep.DeadBranchBytes += ref.Size
		}
		rep.BlobsDeleted += len(deadFreed)
		rep.BlobsRetained += deadCandidates - len(deadFreed)
		r.recordFreed(&rep, deadFreed)
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

// evictBatch removes one batch of releases and deletes the blobs that batch
// left unreferenced. A batch per reason is what makes each reason's own blob and
// byte totals exact: the refcount runs per batch, so a blob freed by this batch
// is attributed to this batch and to nothing else.
func (r *Retention) evictBatch(ctx context.Context, enforce bool, ids []int64) ([]db.BlobRef, int, error) {
	if len(ids) == 0 {
		return nil, 0, nil
	}
	freed, candidates, err := r.db.EvictReleases(ctx, ids, enforce)
	if err != nil {
		return nil, 0, fmt.Errorf("evict releases: %w", err)
	}
	if enforce {
		for _, ref := range freed {
			if err := r.store.Delete(ctx, ref.Key); err != nil {
				// Rows are already committed; a failed blob delete only leaks the
				// blob. It is reported here because nothing else would notice it.
				slog.WarnContext(ctx, "retention: failed to delete freed blob", "key", ref.Key, "err", err)
			}
		}
	}
	return freed, candidates, nil
}

// recordFreed folds a batch's freed blobs into the pass-wide totals.
func (r *Retention) recordFreed(rep *Report, freed []db.BlobRef) {
	for _, ref := range freed {
		rep.ReclaimableBytes += ref.Size
		rep.FreedBlobs = append(rep.FreedBlobs, BlobRef{Key: ref.Key, Size: ref.Size})
	}
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
