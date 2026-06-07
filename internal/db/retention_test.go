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
	return p
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
	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")
	for i := 1; i <= 5; i++ {
		retRelease(t, d, p.ID, fmt.Sprintf("v%d", i), int64(i), "main")
	}
	retRelease(t, d, p.ID, "dev-1", 6, "dev")
	retRelease(t, d, p.ID, "dev-2", 7, "dev")

	future := time.Now().Add(48 * time.Hour)
	got, err := d.ListEvictableReleases(ctx, 2, future)
	require.NoError(t, err)

	var versions []string
	for _, r := range got {
		versions = append(versions, r.Version)
	}
	// main keeps the 2 newest (v4,v5) -> v1,v2,v3 evictable; dev (<=N) fully kept.
	assert.ElementsMatch(t, []string{"v1", "v2", "v3"}, versions)

	// A cutoff in the past excludes everything (recency guard): all rows are fresh.
	got, err = d.ListEvictableReleases(ctx, 2, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestListEvictableReleases_Pins(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")
	r1 := retRelease(t, d, p.ID, "v1", 1, "main")
	r2 := retRelease(t, d, p.ID, "v2", 2, "main")
	retRelease(t, d, p.ID, "v3", 3, "main")
	retRelease(t, d, p.ID, "v4", 4, "main")
	future := time.Now().Add(48 * time.Hour)

	// keep-N=2 normally evicts v1,v2.
	got, err := d.ListEvictableReleases(ctx, 2, future)
	require.NoError(t, err)
	assert.Len(t, got, 2)

	// Pin v1 with a tag and make v2 a docker build -> both excluded.
	require.NoError(t, d.SetOCITag(ctx, p.ID, "latest", "sha256:abc", r1.ID))
	require.NoError(t, d.CreateArtifact(ctx, &Artifact{
		ReleaseID: r2.ID, OS: OSLinux, Arch: ArchAMD64, Kind: KindDocker, StorageKey: "dockerkey", Size: 1, SHA256: "x",
	}))
	got, err = d.ListEvictableReleases(ctx, 2, future)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestEvictReleases_SharedBlobAndCascade(t *testing.T) {
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
	d := openTestDB(t)
	freed, candidates, err := d.EvictReleases(context.Background(), nil, true)
	require.NoError(t, err)
	assert.Empty(t, freed)
	assert.Equal(t, 0, candidates)
}

func TestIsBlobReferenced_AllColumns(t *testing.T) {
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

	for _, key := range []string{"akey", "skey", "dkey", "pkey", "ocikey", "sitekey"} {
		ref, err := d.IsBlobReferenced(ctx, key)
		require.NoError(t, err)
		assert.True(t, ref, "expected %s referenced", key)
	}
}

func TestSumReclaimableBytes(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")
	for i := 1; i <= 5; i++ {
		r := retRelease(t, d, p.ID, fmt.Sprintf("v%d", i), int64(i), "main")
		a := retArtifact(t, d, r.ID, fmt.Sprintf("k%d", i), 100)
		require.NoError(t, d.UpdateArtifactStripped(ctx, a.ID, fmt.Sprintf("s%d", i), 10, "x", fmt.Sprintf("d%d", i), 5))
		require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "deb", fmt.Sprintf("p%d", i), 20, "x", "f", "{}"))
	}
	future := time.Now().Add(48 * time.Hour)
	sum, err := d.SumReclaimableBytes(ctx, 2, future)
	require.NoError(t, err)
	// Evict v1,v2,v3: each = artifact 100 + stripped 10 + debug 5 + packaged 20 = 135; x3 = 405.
	assert.Equal(t, int64(405), sum)
}

func TestListAbandonedReleases(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")

	unpub := &Release{ProjectID: p.ID, Version: "u1", VersionNum: 1, GitBranch: "main"}
	require.NoError(t, d.CreateRelease(ctx, unpub)) // published = 0
	retRelease(t, d, p.ID, "v1", 2, "main")         // published

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
