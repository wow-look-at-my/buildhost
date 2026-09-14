package retention

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/go-containers/set"
)

const testRepo = "wow-look-at-my/proj"

// fakeBranches stands in for GitHub. A repo present in live answers with its
// branch set; a repo present in fail answers with an error; an unlisted repo
// also errors, so a test that forgets to describe a repo fails closed exactly
// as production does.
type fakeBranches struct {
	live  map[string][]string
	fail  map[string]error
	calls map[string]int
}

func (f *fakeBranches) ListBranches(_ context.Context, repoPath string) (set.Set[string], error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[repoPath]++
	if err, ok := f.fail[repoPath]; ok {
		return set.Set[string]{}, err
	}
	names, ok := f.live[repoPath]
	if !ok {
		return set.Set[string]{}, fmt.Errorf("unknown repo %q", repoPath)
	}
	s := set.New[string](len(names))
	s.AddRange(names...)
	return s, nil
}

func branchesLive(names ...string) *fakeBranches {
	return &fakeBranches{live: map[string][]string{testRepo: names}}
}

// deletedBranchSetup builds a project that the deleted-branch rule can act on:
// it has a GitHub repo to ask about and 'master' as its default branch.
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
func agedRelease(t *testing.T, d *db.DB, store storage.Storage, projectID int64, version string, num int64, branch string, age time.Duration) (int64, string) {
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
	return r.ID, key
}

// deletedBranchEngine isolates the new rule: keep-N is set far out of reach, so
// anything the pass removes was removed because its branch is gone.
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

func TestDeletedBranch_LiveBranchIsKeptAtAnyAge(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	_, oldKey := agedRelease(t, d, store, p.ID, "v1", 1, "feature", 400*24*time.Hour)
	_, tipKey := agedRelease(t, d, store, p.ID, "v2", 2, "feature", 399*24*time.Hour)

	rep, err := deletedBranchEngine(d, store, branchesLive("master", "feature")).Run(ctx)
	require.NoError(t, err)

	assert.Empty(t, rep.DeletedBranchReleases, "a build on a live branch is never eligible")
	assert.Equal(t, 0, rep.BlobsDeleted)
	for name, key := range map[string]string{"v1": oldKey, "v2": tipKey} {
		ex, _ := store.Exists(ctx, key)
		assert.True(t, ex, "%s must survive", name)
	}
}

func TestDeletedBranch_OldBuildOnDeletedBranchIsRemoved(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	oldID, oldKey := agedRelease(t, d, store, p.ID, "v1", 1, "gone", 31*24*time.Hour)
	_, tipKey := agedRelease(t, d, store, p.ID, "v2", 2, "gone", 31*24*time.Hour)

	rep, err := deletedBranchEngine(d, store, branchesLive("master")).Run(ctx)
	require.NoError(t, err)

	require.Len(t, rep.DeletedBranchReleases, 1)
	assert.Equal(t, oldID, rep.DeletedBranchReleases[0].ID)
	assert.Equal(t, "gone", rep.DeletedBranchReleases[0].Branch)
	assert.Greater(t, rep.ReclaimableBytes, int64(0))

	_, err = d.GetRelease(ctx, p.ID, "v1")
	assert.ErrorIs(t, err, db.ErrNotFound)
	ex, _ := store.Exists(ctx, oldKey)
	assert.False(t, ex, "the reclaimed build's bytes must be gone")

	ex, _ = store.Exists(ctx, tipKey)
	assert.True(t, ex, "the branch tip backs a dl slot and must survive")
}

func TestDeletedBranch_BuildInsideTheWindowIsKept(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	_, youngKey := agedRelease(t, d, store, p.ID, "v1", 1, "gone", 29*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "gone", 29*24*time.Hour)

	rep, err := deletedBranchEngine(d, store, branchesLive("master")).Run(ctx)
	require.NoError(t, err)

	assert.Empty(t, rep.DeletedBranchReleases, "29 days is inside the 30-day window")
	_, err = d.GetRelease(ctx, p.ID, "v1")
	assert.NoError(t, err)
	ex, _ := store.Exists(ctx, youngKey)
	assert.True(t, ex)
}

// The slot guard is the load-bearing one: dl.{domain}/{project} resolves the
// newest published release on the default branch, and ?branch={branch} resolves
// the newest on that branch. Neither may be reclaimed, whatever its age and
// whatever became of the branch on the remote.
func TestDeletedBranch_SlotReferencedBuildsAreKept(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	// Only build on a branch that is gone: it is that branch's tip.
	_, loneTip := agedRelease(t, d, store, p.ID, "v1", 1, "gone", 900*24*time.Hour)
	// Two builds on the default branch, which is itself absent from the remote.
	_, defaultOld := agedRelease(t, d, store, p.ID, "v2", 2, "master", 900*24*time.Hour)
	_, defaultTip := agedRelease(t, d, store, p.ID, "v3", 3, "master", 900*24*time.Hour)

	rep, err := deletedBranchEngine(d, store, branchesLive("some-other-branch")).Run(ctx)
	require.NoError(t, err)

	assert.Empty(t, rep.DeletedBranchReleases)
	assert.Equal(t, 0, rep.BlobsDeleted)
	for name, key := range map[string]string{"branch tip": loneTip, "default branch": defaultOld, "default tip": defaultTip} {
		ex, _ := store.Exists(ctx, key)
		assert.True(t, ex, "%s must survive", name)
	}
}

func TestDeletedBranch_UndeterminedBranchStateIsKept(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	_, oldKey := agedRelease(t, d, store, p.ID, "v1", 1, "gone", 90*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "gone", 90*24*time.Hour)

	lister := &fakeBranches{fail: map[string]error{testRepo: fmt.Errorf("HTTP 502")}}
	rep, err := deletedBranchEngine(d, store, lister).Run(ctx)
	require.NoError(t, err)

	assert.Empty(t, rep.DeletedBranchReleases, "a failed lookup is not evidence of deletion")
	assert.Equal(t, 1, rep.BranchLookupsFailed)
	require.Len(t, rep.BranchLookupErrors, 1)
	assert.Contains(t, rep.BranchLookupErrors[0], "HTTP 502")

	ex, _ := store.Exists(ctx, oldKey)
	assert.True(t, ex)
}

func TestDeletedBranch_ProjectWithoutRepoIsKeptAndReported(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	_, err := d.Exec("UPDATE projects SET github_repo = '' WHERE id = ?", p.ID)
	require.NoError(t, err)

	_, oldKey := agedRelease(t, d, store, p.ID, "v1", 1, "gone", 90*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "gone", 90*24*time.Hour)

	lister := branchesLive("master")
	rep, err := deletedBranchEngine(d, store, lister).Run(ctx)
	require.NoError(t, err)

	assert.Empty(t, rep.DeletedBranchReleases)
	assert.Equal(t, 1, rep.BranchLookupsFailed)
	require.Len(t, rep.BranchLookupErrors, 1)
	assert.Contains(t, rep.BranchLookupErrors[0], "no github_repo")
	assert.Empty(t, lister.calls, "with no repo recorded there is nothing to ask GitHub about")

	ex, _ := store.Exists(ctx, oldKey)
	assert.True(t, ex)
}

func TestDeletedBranch_ReleaseWithNoRecordedBranchIsReportedNotDeleted(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	id, key := agedRelease(t, d, store, p.ID, "v1", 1, "", 90*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "", 90*24*time.Hour)

	rep, err := deletedBranchEngine(d, store, branchesLive("master")).Run(ctx)
	require.NoError(t, err)

	assert.Empty(t, rep.DeletedBranchReleases)
	require.Len(t, rep.UnknownBranchReleases, 1)
	assert.Equal(t, id, rep.UnknownBranchReleases[0].ID)
	assert.Equal(t, 0, rep.BranchLookupsFailed, "a missing branch is its own case, not a failed lookup")

	_, err = d.GetRelease(ctx, p.ID, "v1")
	assert.NoError(t, err)
	ex, _ := store.Exists(ctx, key)
	assert.True(t, ex)
}

func TestDeletedBranch_WithoutABranchListerNothingIsReclaimed(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	_, oldKey := agedRelease(t, d, store, p.ID, "v1", 1, "gone", 90*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "gone", 90*24*time.Hour)

	rep, err := deletedBranchEngine(d, store, nil).Run(ctx)
	require.NoError(t, err)

	assert.Empty(t, rep.DeletedBranchReleases)
	assert.Equal(t, 1, rep.BranchLookupsFailed)
	require.Len(t, rep.BranchLookupErrors, 1)
	assert.Contains(t, rep.BranchLookupErrors[0], "no branch lister configured")

	ex, _ := store.Exists(ctx, oldKey)
	assert.True(t, ex)
}

func TestDeletedBranch_ZeroWindowDisablesTheRule(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	_, oldKey := agedRelease(t, d, store, p.ID, "v1", 1, "gone", 900*24*time.Hour)
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

// One repository is asked once per pass, however many of its releases are
// candidates, and the answer is not carried across passes.
func TestDeletedBranch_OneLookupPerRepositoryPerPass(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	agedRelease(t, d, store, p.ID, "v1", 1, "gone", 90*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "gone", 90*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v3", 3, "gone", 90*24*time.Hour)

	lister := branchesLive("master")
	_, err := deletedBranchEngine(d, store, lister).Plan(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, lister.calls[testRepo])

	_, err = deletedBranchEngine(d, store, lister).Plan(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, lister.calls[testRepo], "a fresh pass asks again rather than trusting the last answer")
}

// The dry run must name the same releases the enforcing run removes, and leave
// every one of them in place.
func TestDeletedBranch_PlanMatchesRunAndChangesNothing(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	oldID, oldKey := agedRelease(t, d, store, p.ID, "v1", 1, "gone", 90*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "gone", 90*24*time.Hour)

	plan, err := deletedBranchEngine(d, store, branchesLive("master")).Plan(ctx)
	require.NoError(t, err)
	require.Len(t, plan.DeletedBranchReleases, 1)
	assert.Equal(t, oldID, plan.DeletedBranchReleases[0].ID)
	assert.False(t, plan.Enforced)

	ex, _ := store.Exists(ctx, oldKey)
	assert.True(t, ex, "a plan deletes nothing")
	_, err = d.GetRelease(ctx, p.ID, "v1")
	assert.NoError(t, err)

	run, err := deletedBranchEngine(d, store, branchesLive("master")).Run(ctx)
	require.NoError(t, err)
	require.Len(t, run.DeletedBranchReleases, 1)
	assert.Equal(t, plan.DeletedBranchReleases[0].ID, run.DeletedBranchReleases[0].ID)
	assert.Equal(t, plan.ReclaimableBytes, run.ReclaimableBytes)
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

	// v1 and v2 both qualify under keep-N and under the deleted-branch rule.
	assert.Equal(t, 2, rep.Releases())
	assert.Len(t, rep.EvictedReleases, 2)
	assert.Empty(t, rep.DeletedBranchReleases)

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

	_, oldKey := agedRelease(t, d, store, p.ID, "v1", 1, "gone", 90*24*time.Hour)
	agedRelease(t, d, store, p.ID, "v2", 2, "gone", 90*24*time.Hour)

	inv, err := deletedBranchEngine(d, store, branchesLive("master")).Inventory(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, inv.Totals.HoldMismatches)
	assert.Equal(t, 1, inv.Totals.EvictedReleases)

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
