package retention

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func entryFor(t *testing.T, inv Inventory, version, role string) FileEntry {
	t.Helper()
	for _, f := range inv.Files {
		if f.Version == version && f.Role == role {
			return f
		}
	}
	t.Fatalf("no %s entry for %s in %d files", role, version, len(inv.Files))
	return FileEntry{}
}

// The inventory must name the pin on every file it keeps, and agree with the
// plan on what comes back. A hold reason that disagrees with the eviction
func TestInventory_NamesTheHoldOnEveryFile(t *testing.T) {
	d, store, p := setup(t)
	ctx := context.Background()

	putRelease(t, d, store, p.ID, "v1", 1, "main", "one")
	putRelease(t, d, store, p.ID, "v2", 2, "main", "two")
	putRelease(t, d, store, p.ID, "v3", 3, "main", "three")

	ret := New(d, store, Config{KeepN: 1, RecencyGuard: 24 * time.Hour})
	ret.clock = futureClock()

	inv, err := ret.Inventory(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, inv.Totals.HoldMismatches)
	assert.Equal(t, 3, inv.Totals.Releases)
	assert.Equal(t, 2, inv.Totals.EvictedReleases)
	assert.Equal(t, 3, inv.Totals.Files)
	assert.Equal(t, 3, inv.Totals.Blobs)

	tip := entryFor(t, inv, "v3", RoleArtifact)
	assert.Equal(t, HoldBranchTip, tip.Hold)
	assert.Equal(t, []string{HoldBranchTip}, tip.Holds)
	assert.False(t, tip.Reclaimable)
	assert.Equal(t, "proj", tip.Project)
	assert.Equal(t, "main", tip.Branch)
	assert.Equal(t, int64(len("three")), tip.Size)
	assert.Equal(t, 1, tip.Refs)
	assert.False(t, tip.CreatedAt.IsZero())

	for _, v := range []string{"v1", "v2"} {
		e := entryFor(t, inv, v, RoleArtifact)
		assert.True(t, e.Reclaimable, "%s should be reclaimable", v)
		assert.Equal(t, HoldNone, e.Hold)
	}

	plan, err := ret.Plan(ctx)
	require.NoError(t, err)
	assert.Equal(t, plan.ReclaimableBytes, inv.Totals.ReclaimableBytes)
	assert.Equal(t, plan.BlobsDeleted, inv.Totals.ReclaimableBlobs)
}

// The case a reclaimable total never explains: the release goes, but another
// release still references the same content-addressed blob, so nothing is
// freed. The entry must say shared-blob, not report itself reclaimable.
func TestInventory_SharedBlobReportsWhoStillHoldsIt(t *testing.T) {
	d, store, p := setup(t)
	ctx := context.Background()

	putRelease(t, d, store, p.ID, "v1", 1, "main", "same")
	putRelease(t, d, store, p.ID, "v2", 2, "main", "other")
	putRelease(t, d, store, p.ID, "v3", 3, "main", "same")

	ret := New(d, store, Config{KeepN: 1, RecencyGuard: 24 * time.Hour})
	ret.clock = futureClock()

	inv, err := ret.Inventory(ctx)
	require.NoError(t, err)

	old := entryFor(t, inv, "v1", RoleArtifact)
	assert.Equal(t, HoldSharedBlob, old.Hold)
	assert.False(t, old.Reclaimable)
	assert.Equal(t, 2, old.Refs, "the tip references the same blob")

	tip := entryFor(t, inv, "v3", RoleArtifact)
	assert.Equal(t, HoldBranchTip, tip.Hold)
	assert.Equal(t, old.StorageKey, tip.StorageKey)

	// Only v2's blob comes back, so the shared bytes must not be counted.
	assert.Equal(t, int64(len("other")), inv.Totals.ReclaimableBytes)
	assert.Equal(t, 0, inv.Totals.HoldMismatches)
}

// A release inside the recency guard is pinned by time, not by keep-N. The
func TestInventory_RecencyGuardIsItsOwnHold(t *testing.T) {
	d, store, p := setup(t)
	ctx := context.Background()

	putRelease(t, d, store, p.ID, "v1", 1, "main", "one")
	putRelease(t, d, store, p.ID, "v2", 2, "main", "two")

	// The real clock: both releases were created seconds ago.
	inv, err := New(d, store, Config{KeepN: 0, RecencyGuard: 24 * time.Hour}).Inventory(ctx)
	require.NoError(t, err)

	old := entryFor(t, inv, "v1", RoleArtifact)
	assert.Equal(t, HoldRecency, old.Hold)
	assert.Equal(t, []string{HoldRecency}, old.Holds)

	tip := entryFor(t, inv, "v2", RoleArtifact)
	assert.Equal(t, HoldBranchTip, tip.Hold)
	assert.Equal(t, []string{HoldBranchTip, HoldRecency}, tip.Holds)

	assert.Equal(t, int64(0), inv.Totals.ReclaimableBytes)
	assert.Equal(t, 0, inv.Totals.HoldMismatches)
}

func TestInventory_GroupsBiggestHoldFirst(t *testing.T) {
	d, store, p := setup(t)
	ctx := context.Background()

	putRelease(t, d, store, p.ID, "v1", 1, "main", "small")
	putRelease(t, d, store, p.ID, "v2", 2, "main", "a much larger payload than the others")

	ret := New(d, store, Config{KeepN: 1, RecencyGuard: 24 * time.Hour})
	ret.clock = futureClock()

	inv, err := ret.Inventory(ctx)
	require.NoError(t, err)

	require.NotEmpty(t, inv.ByHold)
	assert.Equal(t, HoldBranchTip, inv.ByHold[0].Name)
	assert.Equal(t, int64(len("a much larger payload than the others")), inv.ByHold[0].Bytes)

	require.Len(t, inv.ByRole, 1)
	assert.Equal(t, RoleArtifact, inv.ByRole[0].Name)
	assert.Equal(t, 2, inv.ByRole[0].Files)
	assert.Equal(t, inv.Totals.Bytes, inv.ByRole[0].Bytes)

	require.Len(t, inv.Files, 2)
	assert.Equal(t, "v2", inv.Files[0].Version)
}
