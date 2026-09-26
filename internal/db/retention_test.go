package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func retProject(t *testing.T, d *DB, name string) *Project {
	t.Helper()
	p := &Project{Name: name, Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(context.Background(), p))
	require.NoError(t, d.SetProjectDefaultBranch(context.Background(), p.ID, "main"))
	p.DefaultBranch = "main"
	return p
}

// evictPolicy is a policy whose cutoffs admit every release created before the
// test ran, so only the keep windows and the pins decide.
func evictPolicy(keepN, branchKeepN int64) EvictionPolicy {
	future := time.Now().Add(48 * time.Hour)
	return EvictionPolicy{KeepN: keepN, BranchKeepN: branchKeepN, RecencyCutoff: future, BranchTTLCutoff: future}
}

func versionsOf(rows []ListEvictableReleasesRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Version)
	}
	return out
}

func retRelease(t *testing.T, d *DB, projectID int64, version string, num int64, branch string) *Release {
	t.Helper()
	r := &Release{ProjectID: projectID, Version: version, VersionNum: num, GitBranch: branch}
	require.NoError(t, d.CreateRelease(context.Background(), r))
	require.NoError(t, d.PublishRelease(context.Background(), r.ID))
	return r
}

func retArtifact(t *testing.T, d *DB, releaseID int64, key string, size int64) *Artifact {
	t.Helper()
	a := &Artifact{ReleaseID: releaseID, OS: OSLinux, Arch: ArchAMD64, Kind: KindBinary, StorageKey: key, Size: size, SHA256: key, Filename: "bin"}
	require.NoError(t, d.CreateArtifact(context.Background(), a))
	return a
}

func keysOf(refs []BlobRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Key)
	}
	return out
}

func TestListEvictableReleases_KeepNPerBranch(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")
	for i := 1; i <= 5; i++ {
		retRelease(t, d, p.ID, fmt.Sprintf("v%d", i), int64(i), "main")
	}
	retRelease(t, d, p.ID, "dev-1", 6, "dev")
	retRelease(t, d, p.ID, "dev-2", 7, "dev")
	retRelease(t, d, p.ID, "dev-3", 8, "dev")

	got, err := d.ListEvictableReleases(ctx, evictPolicy(2, 2))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"v1", "v2", "v3", "dev-1"}, versionsOf(got))

	// The default branch and the other branches have separate windows.
	got, err = d.ListEvictableReleases(ctx, evictPolicy(2, 1))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"v1", "v2", "v3", "dev-1", "dev-2"}, versionsOf(got))

	// A cutoff in the past excludes everything (recency guard): all rows are fresh.
	p2 := evictPolicy(2, 1)
	p2.RecencyCutoff = time.Now().Add(-time.Hour)
	got, err = d.ListEvictableReleases(ctx, p2)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestListEvictableReleases_DeletedBranchTTL(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")
	retRelease(t, d, p.ID, "v1", 1, "main")
	retRelease(t, d, p.ID, "gone-1", 2, "gone")
	retRelease(t, d, p.ID, "gone-2", 3, "gone")
	retRelease(t, d, p.ID, "live-1", 4, "live")
	retRelease(t, d, p.ID, "v2", 5, "main")

	// Not deleted: the tip of each branch stays.
	pol := evictPolicy(10, 1)
	got, err := d.ListEvictableReleases(ctx, pol)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"gone-1"}, versionsOf(got))

	deletedAt := time.Now().Add(-72 * time.Hour)
	require.NoError(t, d.RecordBranchDeleted(ctx, p.ID, "gone", deletedAt))
	pol.BranchTTLCutoff = time.Now().Add(-7 * 24 * time.Hour)
	got, err = d.ListEvictableReleases(ctx, pol)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"gone-1"}, versionsOf(got))

	// Past the TTL: the whole branch goes, tip included. Other branches stay.
	pol.BranchTTLCutoff = time.Now().Add(-24 * time.Hour)
	got, err = d.ListEvictableReleases(ctx, pol)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"gone-1", "gone-2"}, versionsOf(got))

	sum, err := d.SumReclaimableBytes(ctx, pol)
	require.NoError(t, err)
	assert.Equal(t, int64(0), sum) // no artifacts in this test

	// A repeated record keeps the earliest deletion time.
	require.NoError(t, d.RecordBranchDeleted(ctx, p.ID, "gone", time.Now()))
	got, err = d.ListEvictableReleases(ctx, pol)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"gone-1", "gone-2"}, versionsOf(got))

	// The branch comes back: its tip is pinned again.
	require.NoError(t, d.ClearBranchDeleted(ctx, p.ID, "gone"))
	got, err = d.ListEvictableReleases(ctx, pol)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"gone-1"}, versionsOf(got))
}

func TestListEvictableReleases_ProjectNewestSurvivesDeletion(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")
	retRelease(t, d, p.ID, "f1", 1, "feature")
	retRelease(t, d, p.ID, "f2", 2, "feature")
	require.NoError(t, d.RecordBranchDeleted(ctx, p.ID, "feature", time.Now().Add(-30*24*time.Hour)))

	pol := evictPolicy(10, 1)
	pol.BranchTTLCutoff = time.Now()
	got, err := d.ListEvictableReleases(ctx, pol)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"f1"}, versionsOf(got))
}

func TestRecordBranchDeletedForRepo(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")
	other := retProject(t, d, "other")
	require.NoError(t, d.SetProjectGitHubRepo(ctx, p.ID, "Org/Proj"))
	require.NoError(t, d.SetProjectGitHubRepo(ctx, other.ID, "org/other"))

	require.NoError(t, d.RecordBranchDeletedForRepo(ctx, "org/proj", "feat", time.Now()))
	got, err := d.ListDeletedBranches(ctx, p.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "feat", got[0].Branch)
	got, err = d.ListDeletedBranches(ctx, other.ID)
	require.NoError(t, err)
	assert.Empty(t, got)

	require.NoError(t, d.ClearBranchDeletedForRepo(ctx, "org/proj", "feat"))
	got, err = d.ListDeletedBranches(ctx, p.ID)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestListEvictableReleases_KeepZeroStillKeepsTip(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")
	retRelease(t, d, p.ID, "v1", 1, "main")
	retRelease(t, d, p.ID, "v2", 2, "main")
	retRelease(t, d, p.ID, "v3", 3, "main")
	retRelease(t, d, p.ID, "f1", 4, "feature")
	retRelease(t, d, p.ID, "v4", 5, "main")

	got, err := d.ListEvictableReleases(ctx, evictPolicy(0, 0))
	require.NoError(t, err)
	versions := versionsOf(got)
	assert.ElementsMatch(t, []string{"v1", "v2", "v3"}, versions)
	assert.NotContains(t, versions, "v4")
	assert.NotContains(t, versions, "f1")
}

func TestListEvictableReleases_Pins(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")
	r1 := retRelease(t, d, p.ID, "v1", 1, "main")
	r2 := retRelease(t, d, p.ID, "v2", 2, "main")
	retRelease(t, d, p.ID, "v3", 3, "main")
	retRelease(t, d, p.ID, "v4", 4, "main")

	got, err := d.ListEvictableReleases(ctx, evictPolicy(2, 2))
	require.NoError(t, err)
	assert.Len(t, got, 2)

	// Pin v1 with a tag and make v2 a docker build -> both excluded.
	require.NoError(t, d.SetOCITag(ctx, p.ID, "latest", "sha256:abc", r1.ID))
	require.NoError(t, d.CreateArtifact(ctx, &Artifact{
		ReleaseID: r2.ID, OS: OSLinux, Arch: ArchAMD64, Kind: KindDocker, StorageKey: "dockerkey", Size: 1, SHA256: "x",
	}))
	got, err = d.ListEvictableReleases(ctx, evictPolicy(2, 2))
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestEvictReleases_SharedBlobAndCascade(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")
	r1 := retRelease(t, d, p.ID, "v1", 1, "main")
	r2 := retRelease(t, d, p.ID, "v2", 2, "main")

	a1 := retArtifact(t, d, r1.ID, "shared", 100)
	require.NoError(t, d.UpdateArtifactStripped(ctx, a1.ID, "strip1", 50, "s1", "dbg1", 30))
	require.NoError(t, d.CreatePackagedArtifact(ctx, a1.ID, "deb", "pkg1", 70, "p1", "f.deb", "{}"))
	require.NoError(t, d.IncrementDownloadCount(ctx, a1.ID))
	require.NoError(t, d.SetOCITag(ctx, p.ID, "v1tag", "sha256:x", r1.ID))

	// r2 shares the "shared" blob (identical content, deduplicated).
	retArtifact(t, d, r2.ID, "shared", 100)

	freed, candidates, err := d.EvictReleases(ctx, []int64{r1.ID}, true)
	require.NoError(t, err)
	assert.Equal(t, 4, candidates) // shared, strip1, dbg1, pkg1
	keys := keysOf(freed)
	assert.NotContains(t, keys, "shared") // still referenced by r2
	assert.ElementsMatch(t, []string{"strip1", "dbg1", "pkg1"}, keys)

	// r1 and all its child rows are gone; r2 untouched.
	_, err = d.GetRelease(ctx, p.ID, "v1")
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = d.GetRelease(ctx, p.ID, "v2")
	assert.NoError(t, err)
	_, err = d.GetOCITag(ctx, p.ID, "v1tag")
	assert.ErrorIs(t, err, ErrNotFound)

	// Evicting r2 now frees the shared blob.
	freed2, _, err := d.EvictReleases(ctx, []int64{r2.ID}, true)
	require.NoError(t, err)
	assert.Contains(t, keysOf(freed2), "shared")
}

func TestEvictReleases_DryRunChangesNothing(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")
	r := retRelease(t, d, p.ID, "v1", 1, "main")
	retArtifact(t, d, r.ID, "k1", 100)

	freed, candidates, err := d.EvictReleases(ctx, []int64{r.ID}, false)
	require.NoError(t, err)
	assert.Equal(t, 1, candidates)
	assert.Equal(t, []string{"k1"}, keysOf(freed)) // would free

	// Rolled back: the release is still there.
	_, err = d.GetRelease(ctx, p.ID, "v1")
	assert.NoError(t, err)
}

func TestEvictReleases_Empty(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	freed, candidates, err := d.EvictReleases(context.Background(), nil, true)
	require.NoError(t, err)
	assert.Empty(t, freed)
	assert.Equal(t, 0, candidates)
}

func TestIsBlobReferenced_AllColumns(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")
	r := retRelease(t, d, p.ID, "v1", 1, "main")

	ref, err := d.IsBlobReferenced(ctx, "absent")
	require.NoError(t, err)
	assert.False(t, ref)

	a := retArtifact(t, d, r.ID, "akey", 10)
	require.NoError(t, d.UpdateArtifactStripped(ctx, a.ID, "skey", 5, "s", "dkey", 3))
	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "deb", "pkey", 7, "p", "f", "{}"))
	require.NoError(t, d.LinkOCIBlob(ctx, p.ID, "ocikey", "", 9, false))
	_, err = d.UpsertSite(ctx, &Site{ProjectID: p.ID, Branch: "main", StorageKey: "sitekey", Size: 11, SHA256: "s"})
	require.NoError(t, err)

	// A cached Go module zip is a reference too. It hangs off no project and no
	modID, err := d.GoproxyModuleID(ctx, "github.com/o/r", "github")
	require.NoError(t, err)
	require.NoError(t, d.PutGoproxyCached(ctx, modID, &GoproxyCached{
		Version: "v1.0.0", ZipKey: "gomodkey", ZipSize: 13,
	}))

	for _, key := range []string{"akey", "skey", "dkey", "pkey", "ocikey", "sitekey", "gomodkey"} {
		ref, err := d.IsBlobReferenced(ctx, key)
		require.NoError(t, err)
		assert.True(t, ref, "expected %s referenced", key)
	}
}

func TestSumReclaimableBytes(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")
	for i := 1; i <= 5; i++ {
		r := retRelease(t, d, p.ID, fmt.Sprintf("v%d", i), int64(i), "main")
		a := retArtifact(t, d, r.ID, fmt.Sprintf("k%d", i), 100)
		require.NoError(t, d.UpdateArtifactStripped(ctx, a.ID, fmt.Sprintf("s%d", i), 10, "x", fmt.Sprintf("d%d", i), 5))
		require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "deb", fmt.Sprintf("p%d", i), 20, "x", "f", "{}"))
	}
	sum, err := d.SumReclaimableBytes(ctx, evictPolicy(2, 2))
	require.NoError(t, err)
	assert.Equal(t, int64(405), sum)
}

func TestRetentionSettings(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	// Defaults when unseeded.
	s, err := d.GetRetentionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, DefaultRetentionSettings, s)

	first := RetentionSettings{KeepN: 5, RecencyHours: 12, BranchKeepN: 2, BranchTTLDays: 3}
	require.NoError(t, d.SeedRetentionSettings(ctx, first))
	require.NoError(t, d.SeedRetentionSettings(ctx, RetentionSettings{KeepN: 99, RecencyHours: 99, BranchKeepN: 99, BranchTTLDays: 99}))
	s, err = d.GetRetentionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, first, s)

	// Update overwrites.
	next := RetentionSettings{KeepN: 20, RecencyHours: 48, BranchKeepN: 0, BranchTTLDays: 14}
	require.NoError(t, d.UpdateRetentionSettings(ctx, next))
	s, err = d.GetRetentionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, next, s)
}

func TestUpdateRetentionSettings_UpsertsWhenUnseeded(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	// No prior seed: the update must insert the row (upsert), not no-op.
	want := RetentionSettings{KeepN: 3, RecencyHours: 6, BranchKeepN: 1, BranchTTLDays: 7}
	require.NoError(t, d.UpdateRetentionSettings(ctx, want))
	s, err := d.GetRetentionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, want, s)
}

func TestListAbandonedReleases(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")

	unpub := &Release{ProjectID: p.ID, Version: "u1", VersionNum: 1, GitBranch: "main"}
	require.NoError(t, d.CreateRelease(ctx, unpub))
	retRelease(t, d, p.ID, "v1", 2, "main") // published

	future := time.Now().Add(48 * time.Hour)
	got, err := d.ListAbandonedReleases(ctx, future)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "u1", got[0].Version)

	// Fresh unpublished release is protected by an earlier cutoff.
	got, err = d.ListAbandonedReleases(ctx, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Empty(t, got)
}
