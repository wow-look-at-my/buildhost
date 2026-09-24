package retention

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeLister(branches map[string][]string) BranchLister {
	return func(_ context.Context, repo string) ([]string, error) {
		b, ok := branches[repo]
		if !ok {
			return nil, errors.New("no such repo: " + repo)
		}
		return b, nil
	}
}

func TestRun_DeletedBranchKeptForTTLThenEvicted(t *testing.T) {
	t.Serial()
	d, store, p := setup(t)
	ctx := context.Background()
	require.NoError(t, d.SetProjectGitHubRepo(ctx, p.ID, "org/proj"))

	putRelease(t, d, store, p.ID, "v1", 1, "main", "m1")
	putRelease(t, d, store, p.ID, "g1", 2, "gone", "g1")
	gone := putRelease(t, d, store, p.ID, "g2", 3, "gone", "g2")
	putRelease(t, d, store, p.ID, "l1", 4, "live", "l1")
	putRelease(t, d, store, p.ID, "v2", 5, "main", "m2")

	now := time.Now().Add(time.Hour)
	cfg := Config{KeepN: 10, BranchKeepN: 1, BranchTTL: 7 * 24 * time.Hour, RecencyGuard: 0, Enforce: true}
	lister := fakeLister(map[string][]string{"org/proj": {"main", "live"}})

	ret := New(d, store, cfg).WithBranchLister(lister)
	ret.clock = func() time.Time { return now }
	rep, err := ret.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.BranchSync.Repos)
	assert.Equal(t, 1, rep.BranchSync.MarkedDeleted)
	assert.Empty(t, rep.BranchSync.Errors)
	// Only the release past branch keep-N goes; the deleted branch's tip is inside the TTL.
	require.Len(t, rep.EvictedReleases, 1)
	assert.Equal(t, "g1", rep.EvictedReleases[0].Version)

	ret.clock = func() time.Time { return now.Add(6 * 24 * time.Hour) }
	rep, err = ret.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, rep.BranchSync.MarkedDeleted) // the earliest sighting is
	assert.Empty(t, rep.EvictedReleases)

	// Past the TTL the tip goes. The live branch and the default branch stay.
	ret.clock = func() time.Time { return now.Add(8 * 24 * time.Hour) }
	rep, err = ret.Run(ctx)
	require.NoError(t, err)
	require.Len(t, rep.EvictedReleases, 1)
	assert.Equal(t, "g2", rep.EvictedReleases[0].Version)
	ex, _ := store.Exists(ctx, gone)
	assert.False(t, ex)
	for _, v := range []string{"v1", "v2", "l1"} {
		_, err := d.GetRelease(ctx, p.ID, v)
		assert.NoError(t, err, v)
	}
}

func TestSyncDeletedBranches_ClearsWhenBranchReturns(t *testing.T) {
	t.Serial()
	d, store, p := setup(t)
	ctx := context.Background()
	require.NoError(t, d.SetProjectGitHubRepo(ctx, p.ID, "org/proj"))
	putRelease(t, d, store, p.ID, "f1", 1, "feat", "f1")
	putRelease(t, d, store, p.ID, "v1", 2, "main", "v1")

	ret := New(d, store, Config{}).WithBranchLister(fakeLister(map[string][]string{"org/proj": {"main"}}))
	rep, err := ret.SyncDeletedBranches(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.MarkedDeleted)

	ret.WithBranchLister(fakeLister(map[string][]string{"org/proj": {"main", "feat"}}))
	rep, err = ret.SyncDeletedBranches(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Cleared)
	got, err := d.ListDeletedBranches(ctx, p.ID)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSyncDeletedBranches_ListFailureMarksNothing(t *testing.T) {
	t.Serial()
	d, store, p := setup(t)
	ctx := context.Background()
	require.NoError(t, d.SetProjectGitHubRepo(ctx, p.ID, "org/proj"))
	putRelease(t, d, store, p.ID, "f1", 1, "feat", "f1")

	ret := New(d, store, Config{}).WithBranchLister(fakeLister(nil))
	rep, err := ret.SyncDeletedBranches(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, rep.MarkedDeleted)
	require.Len(t, rep.Errors, 1)
	assert.Contains(t, rep.Errors[0], "org/proj")
	got, err := d.ListDeletedBranches(ctx, p.ID)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSyncDeletedBranches_NeverMarksDefaultBranch(t *testing.T) {
	t.Serial()
	d, store, p := setup(t)
	ctx := context.Background()
	require.NoError(t, d.SetProjectGitHubRepo(ctx, p.ID, "org/proj"))
	putRelease(t, d, store, p.ID, "v1", 1, "main", "v1")

	ret := New(d, store, Config{}).WithBranchLister(fakeLister(map[string][]string{"org/proj": {}}))
	rep, err := ret.SyncDeletedBranches(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, rep.MarkedDeleted)
}
