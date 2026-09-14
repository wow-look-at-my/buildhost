package retention

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/go-containers/set"
)

// captureLogs routes slog output to a buffer for the duration of the test, so
// the line an operator would see for a repository that could not be asked can be
// asserted on rather than assumed.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// deletedBranchSetup builds a project the deleted-branch rule can act on: it is
// linked to a GitHub repository and its default branch is 'master'.
func deletedBranchSetup(t *testing.T) (*db.DB, *storage.Filesystem, *db.Project) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)

	p := &db.Project{Name: "proj", Versioning: db.VersioningAuto, GithubRepo: testRepo}
	require.NoError(t, d.CreateProject(context.Background(), p))
	require.NoError(t, d.SetProjectDefaultBranch(context.Background(), p.ID, "master"))
	p.DefaultBranch = "master"
	return d, store, p
}

// agedRelease publishes a release with one artifact and rewrites its created_at
// to the requested age, which is the input the deleted-branch window reads.
func agedRelease(t *testing.T, d *db.DB, store storage.Storage, projectID int64, version string, num int64, branch string, age time.Duration) (int64, string, int64) {
	t.Helper()
	ctx := context.Background()

	key, size, err := store.Put(ctx, bytes.NewReader([]byte("payload-"+version)))
	require.NoError(t, err)

	r := &db.Release{ProjectID: projectID, Version: version, VersionNum: num, GitBranch: branch}
	require.NoError(t, d.CreateRelease(ctx, r))
	require.NoError(t, d.PublishRelease(ctx, r.ID))
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: r.ID, OS: db.OSLinux, Arch: db.ArchAMD64, Kind: db.KindBinary,
		StorageKey: key, Size: size, SHA256: key, Filename: "bin",
	}))

	stamp := time.Now().Add(-age).UTC().Format("2006-01-02 15:04:05")
	_, err = d.Exec("UPDATE releases SET created_at = ? WHERE id = ?", stamp, r.ID)
	require.NoError(t, err)
	return r.ID, key, size
}

// deletedBranchEngine isolates the new rule: keep-N is set far out of reach and
// the recency guard is left at its default, so anything the pass removes was
// removed because its branch is gone.
func deletedBranchEngine(d *db.DB, store storage.Storage, lister BranchLister) *Retention {
	ret := New(d, store, Config{
		KeepN:            1000,
		RecencyGuard:     24 * time.Hour,
		Enforce:          true,
		DeletedBranchAge: 30 * 24 * time.Hour,
	})
	if lister != nil {
		ret = ret.WithBranchLister(lister)
	}
	return ret
}

// The headline case: a branch that no longer exists on the remote ages out
// completely. There is no tip exemption, so the branch's newest build goes too.
func TestDeletedBranch_DeletedBranchAgesOutCompletely(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	var wantBytes int64
	var versions, keys []string
	for i, days := range []int{40, 39, 38} {
		v := fmt.Sprintf("v%d", i+1) // v3 is the branch tip
		_, key, size := agedRelease(t, d, store, p.ID, v, int64(i+1), "gone", time.Duration(days)*24*time.Hour)
		versions = append(versions, v)
		keys = append(keys, key)
		wantBytes += size
	}

	rep, err := deletedBranchEngine(d, store, branchesLive("master")).Run(ctx)
	require.NoError(t, err)

	require.Len(t, rep.DeletedBranchReleases, 3)
	listed := map[string]bool{}
	for _, ref := range rep.DeletedBranchReleases {
		listed[ref.Version] = true
		assert.Equal(t, "gone", ref.Branch)
	}
	assert.True(t, listed["v3"], "the branch tip is not exempt: a dead branch ages out completely")
	assert.Equal(t, 3, rep.Releases())
	assert.Empty(t, rep.EvictedReleases, "keep-N did not touch these")
	assert.Empty(t, rep.AbandonedReleases)

	// Row and blob both go, through the same eviction path keep-N uses.
	for _, v := range versions {
		_, err := d.GetRelease(ctx, p.ID, v)
		assert.ErrorIs(t, err, db.ErrNotFound, "release %s must be gone", v)
	}
	for _, key := range keys {
		ex, _ := store.Exists(ctx, key)
		assert.False(t, ex, "blob %s must be gone", key)
	}

	// The reason carries its own build, blob and byte totals.
	assert.Equal(t, 3, rep.DeadBranchBlobs)
	assert.Equal(t, wantBytes, rep.DeadBranchBytes)
	assert.Equal(t, 3, rep.BlobsDeleted)
	assert.Equal(t, wantBytes, rep.ReclaimableBytes)
}

// A branch the remote still has is untouched, at any age.
func TestDeletedBranch_LiveBranchIsKeptAtAnyAge(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	_, oldKey, _ := agedRelease(t, d, store, p.ID, "v1", 1, "feature", 400*24*time.Hour)
	_, tipKey, _ := agedRelease(t, d, store, p.ID, "v2", 2, "feature", 399*24*time.Hour)

	rep, err := deletedBranchEngine(d, store, branchesLive("master", "feature")).Run(ctx)
	require.NoError(t, err)

	assert.Empty(t, rep.DeletedBranchReleases, "a build on a live branch is never eligible")
	assert.Equal(t, 0, rep.DeadBranchBlobs)
	assert.Equal(t, int64(0), rep.DeadBranchBytes)
	for name, key := range map[string]string{"v1": oldKey, "v2": tipKey} {
		ex, _ := store.Exists(ctx, key)
		assert.True(t, ex, "%s must survive", name)
	}
}

// The 30-day window is its own: a build inside it survives even on a branch that
// is gone.
func TestDeletedBranch_BuildInsideTheWindowIsKept(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	_, youngKey, _ := agedRelease(t, d, store, p.ID, "v1", 1, "gone", 29*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "gone", 29*24*time.Hour)

	rep, err := deletedBranchEngine(d, store, branchesLive("master")).Run(ctx)
	require.NoError(t, err)

	assert.Empty(t, rep.DeletedBranchReleases, "29 days is inside the 30-day window")
	_, err = d.GetRelease(ctx, p.ID, "v1")
	assert.NoError(t, err)
	ex, _ := store.Exists(ctx, youngKey)
	assert.True(t, ex)
}

// The default branch is never dead, even if the remote's answer omits it: the
// apex latest resolves against it.
func TestDeletedBranch_DefaultBranchIsNeverDead(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	_, oldKey, _ := agedRelease(t, d, store, p.ID, "v1", 1, "master", 900*24*time.Hour)
	_, tipKey, _ := agedRelease(t, d, store, p.ID, "v2", 2, "master", 899*24*time.Hour)

	rep, err := deletedBranchEngine(d, store, branchesLive("some-other-branch")).Run(ctx)
	require.NoError(t, err)

	assert.Empty(t, rep.DeletedBranchReleases)
	assert.Equal(t, 0, rep.BlobsDeleted)
	for name, key := range map[string]string{"old": oldKey, "tip": tipKey} {
		ex, _ := store.Exists(ctx, key)
		assert.True(t, ex, "the %s build on the default branch must survive", name)
	}
}

// A repository that cannot be asked is not a repository whose branches were
// deleted. Everything of it is kept, and one line says why.
func TestDeletedBranch_UnreachableRepositoryIsKeptAndLogged(t *testing.T) {
	t.Serial()
	logs := captureLogs(t)
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	_, oldKey, _ := agedRelease(t, d, store, p.ID, "v1", 1, "gone", 90*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "gone", 90*24*time.Hour)

	lister := &fakeBranches{fail: map[string]error{testRepo: fmt.Errorf("HTTP 502")}}
	rep, err := deletedBranchEngine(d, store, lister).Run(ctx)
	require.NoError(t, err)

	assert.Empty(t, rep.DeletedBranchReleases, "a failed lookup is not evidence of deletion")
	assert.Equal(t, 2, rep.BranchLookupsFailed, "both builds of the unreachable repository were kept")
	assert.Len(t, rep.BranchLookupErrors, 1, "one cause, one message")
	assert.Contains(t, rep.BranchLookupErrors[0], testRepo)
	assert.Contains(t, rep.BranchLookupErrors[0], "HTTP 502")

	// The log names the repository and the reason, once rather than per build.
	out := logs.String()
	assert.Equal(t, 1, strings.Count(out, testRepo), "one line per repository, not one per build: %s", out)
	assert.Contains(t, out, "HTTP 502")

	ex, _ := store.Exists(ctx, oldKey)
	assert.True(t, ex)
	_, err = d.GetRelease(ctx, p.ID, "v1")
	assert.NoError(t, err)
}

// A project with no linked repository cannot be asked about at all.
func TestDeletedBranch_ProjectWithoutRepoIsKeptAndReported(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	_, err := d.Exec("UPDATE projects SET github_repo = '' WHERE id = ?", p.ID)
	require.NoError(t, err)

	_, oldKey, _ := agedRelease(t, d, store, p.ID, "v1", 1, "gone", 90*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "gone", 90*24*time.Hour)

	lister := branchesLive("master")
	rep, err := deletedBranchEngine(d, store, lister).Run(ctx)
	require.NoError(t, err)

	assert.Empty(t, rep.DeletedBranchReleases)
	assert.Equal(t, 2, rep.BranchLookupsFailed)
	require.Len(t, rep.BranchLookupErrors, 1)
	assert.Contains(t, rep.BranchLookupErrors[0], "no github_repo")
	assert.Empty(t, lister.calls, "with no repo recorded there is nothing to ask GitHub about")

	ex, _ := store.Exists(ctx, oldKey)
	assert.True(t, ex)
}

// A release with no recorded branch has nothing to check, so it is reported as
// kept rather than deleted on an assumption.
func TestDeletedBranch_ReleaseWithNoRecordedBranchIsReportedNotDeleted(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	id, key, _ := agedRelease(t, d, store, p.ID, "v1", 1, "", 90*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "", 90*24*time.Hour)

	rep, err := deletedBranchEngine(d, store, branchesLive("master")).Run(ctx)
	require.NoError(t, err)

	assert.Empty(t, rep.DeletedBranchReleases)
	require.Len(t, rep.UnknownBranchReleases, 2)
	assert.Equal(t, id, rep.UnknownBranchReleases[0].ID)
	assert.Equal(t, 0, rep.BranchLookupsFailed, "a missing branch is its own case, not a failed lookup")

	_, err = d.GetRelease(ctx, p.ID, "v1")
	assert.NoError(t, err)
	ex, _ := store.Exists(ctx, key)
	assert.True(t, ex)
}

// No source of truth for branch existence means no deletion: the rule is inert
// and the report says so.
func TestDeletedBranch_WithoutABranchListerNothingIsReclaimed(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	_, oldKey, _ := agedRelease(t, d, store, p.ID, "v1", 1, "gone", 90*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "gone", 90*24*time.Hour)

	rep, err := deletedBranchEngine(d, store, nil).Run(ctx)
	require.NoError(t, err)

	assert.Empty(t, rep.DeletedBranchReleases)
	assert.Equal(t, 2, rep.BranchLookupsFailed)
	require.Len(t, rep.BranchLookupErrors, 1)
	assert.Contains(t, rep.BranchLookupErrors[0], "no branch lister configured")

	ex, _ := store.Exists(ctx, oldKey)
	assert.True(t, ex)
}

// A zero window switches the rule off, and it must not even ask GitHub.
func TestDeletedBranch_ZeroWindowDisablesTheRule(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	_, oldKey, _ := agedRelease(t, d, store, p.ID, "v1", 1, "gone", 900*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "gone", 900*24*time.Hour)

	lister := branchesLive("master")
	ret := New(d, store, Config{KeepN: 1000, RecencyGuard: 24 * time.Hour, Enforce: true}).
		WithBranchLister(lister)
	rep, err := ret.Run(ctx)
	require.NoError(t, err)

	assert.Empty(t, rep.DeletedBranchReleases)
	assert.Equal(t, 0, rep.BranchLookupsFailed)
	assert.Empty(t, lister.calls, "a disabled rule must not call GitHub at all")

	ex, _ := store.Exists(ctx, oldKey)
	assert.True(t, ex)
}

// The dry run names the same releases the enforcing run removes, and leaves
// every one of them in place.
func TestDeletedBranch_PlanMatchesRunAndChangesNothing(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	oldID, oldKey, wantBytes := agedRelease(t, d, store, p.ID, "v1", 1, "gone", 90*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "gone", 90*24*time.Hour)

	plan, err := deletedBranchEngine(d, store, branchesLive("master")).Plan(ctx)
	require.NoError(t, err)
	require.Len(t, plan.DeletedBranchReleases, 2)
	assert.Equal(t, oldID, plan.DeletedBranchReleases[0].ID)
	assert.False(t, plan.Enforced)
	assert.Equal(t, wantBytes, plan.DeadBranchBytes)

	ex, _ := store.Exists(ctx, oldKey)
	assert.True(t, ex, "a plan deletes nothing")
	_, err = d.GetRelease(ctx, p.ID, "v1")
	assert.NoError(t, err)

	run, err := deletedBranchEngine(d, store, branchesLive("master")).Run(ctx)
	require.NoError(t, err)
	require.Len(t, run.DeletedBranchReleases, 2)
	assert.Equal(t, plan.DeletedBranchReleases[0].ID, run.DeletedBranchReleases[0].ID)
	assert.Equal(t, plan.ReclaimableBytes, run.ReclaimableBytes)
	assert.Equal(t, plan.DeadBranchBytes, run.DeadBranchBytes)
}

// A release both past keep-N and on a deleted branch is one eviction, counted
// once, so the report's totals match the rows actually removed.
func TestDeletedBranch_OverlapWithKeepNIsCountedOnce(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	agedRelease(t, d, store, p.ID, "v1", 1, "gone", 90*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "gone", 90*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v3", 3, "gone", 90*24*time.Hour)

	ret := New(d, store, Config{
		KeepN:            1,
		RecencyGuard:     24 * time.Hour,
		Enforce:          false,
		DeletedBranchAge: 30 * 24 * time.Hour,
	}).WithBranchLister(branchesLive("master"))

	rep, err := ret.Plan(ctx)
	require.NoError(t, err)

	// v1 and v2 qualify under keep-N as well as under the dead-branch rule; the
	// branch tip v3 qualifies only under the dead-branch rule.
	assert.Equal(t, 3, rep.Releases())
	assert.Len(t, rep.EvictedReleases, 2)
	assert.Len(t, rep.DeletedBranchReleases, 1)

	seen := set.New[int64](rep.Releases())
	for _, ref := range rep.AllEvicted() {
		assert.True(t, seen.Add(ref.ID), "release %d listed twice", ref.ID)
	}
}

// The inventory explains why each file is kept. A build the deleted-branch rule
// takes must read as reclaimable there too, rather than showing a keep-N hold
// the plan disagrees with.
func TestDeletedBranch_InventoryAgreesWithThePlan(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	_, oldKey, _ := agedRelease(t, d, store, p.ID, "v1", 1, "gone", 90*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "gone", 90*24*time.Hour)

	inv, err := deletedBranchEngine(d, store, branchesLive("master")).Inventory(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, inv.Totals.HoldMismatches)
	assert.Equal(t, 2, inv.Totals.EvictedReleases)

	var found bool
	for _, f := range inv.Files {
		if f.StorageKey != oldKey {
			continue
		}
		found = true
		assert.True(t, f.Reclaimable)
		assert.Equal(t, HoldNone, f.Hold)
	}
	assert.True(t, found, "the reclaimed build must appear in the inventory")
}
