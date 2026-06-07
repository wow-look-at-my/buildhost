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
)

func setup(t *testing.T) (*db.DB, *storage.Filesystem, *db.Project) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)

	p := &db.Project{Name: "proj", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(context.Background(), p))
	return d, store, p
}

// putRelease stores unique content and creates a published release + artifact
// pointing at the resulting content-addressed key. Returns the storage key.
func putRelease(t *testing.T, d *db.DB, store storage.Storage, projectID int64, version string, num int64, branch, content string) string {
	t.Helper()
	ctx := context.Background()
	key, size, err := store.Put(ctx, bytes.NewReader([]byte(content)))
	require.NoError(t, err)
	r := &db.Release{ProjectID: projectID, Version: version, VersionNum: num, GitBranch: branch}
	require.NoError(t, d.CreateRelease(ctx, r))
	require.NoError(t, d.PublishRelease(ctx, r.ID))
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: r.ID, OS: db.OSLinux, Arch: db.ArchAMD64, Kind: db.KindBinary,
		StorageKey: key, Size: size, SHA256: key, Filename: "bin",
	}))
	return key
}

// futureClock makes every existing release look "old" relative to the recency
// guard, so eviction is exercised deterministically without sleeping.
func futureClock() func() time.Time {
	return func() time.Time { return time.Now().Add(365 * 24 * time.Hour) }
}

func TestRun_EnforceEvictsPastKeepN(t *testing.T) {
	d, store, p := setup(t)
	ctx := context.Background()

	keys := map[string]string{}
	for i := 1; i <= 5; i++ {
		v := fmt.Sprintf("v%d", i)
		keys[v] = putRelease(t, d, store, p.ID, v, int64(i), "main", "content-"+v)
	}

	ret := New(d, store, Config{KeepN: 2, RecencyGuard: 24 * time.Hour, Enforce: true})
	ret.clock = futureClock()

	rep, err := ret.Run(ctx)
	require.NoError(t, err)

	assert.True(t, rep.Enforced)
	assert.Len(t, rep.EvictedReleases, 3) // v1,v2,v3
	assert.Equal(t, 3, rep.BlobsDeleted)
	assert.Equal(t, 0, rep.BlobsRetained)
	assert.Greater(t, rep.ReclaimableBytes, int64(0))

	for _, v := range []string{"v1", "v2", "v3"} {
		ex, _ := store.Exists(ctx, keys[v])
		assert.False(t, ex, "blob for %s should be deleted", v)
		_, err := d.GetRelease(ctx, p.ID, v)
		assert.ErrorIs(t, err, db.ErrNotFound)
	}
	for _, v := range []string{"v4", "v5"} {
		ex, _ := store.Exists(ctx, keys[v])
		assert.True(t, ex, "blob for %s should remain", v)
		_, err := d.GetRelease(ctx, p.ID, v)
		assert.NoError(t, err)
	}
}

func TestPlan_ReportOnlyChangesNothing(t *testing.T) {
	d, store, p := setup(t)
	ctx := context.Background()

	var keys []string
	for i := 1; i <= 4; i++ {
		v := fmt.Sprintf("v%d", i)
		keys = append(keys, putRelease(t, d, store, p.ID, v, int64(i), "main", "c-"+v))
	}

	ret := New(d, store, Config{KeepN: 2, RecencyGuard: 24 * time.Hour}) // Enforce defaults false
	ret.clock = futureClock()

	rep, err := ret.Plan(ctx)
	require.NoError(t, err)
	assert.False(t, rep.Enforced)
	assert.Len(t, rep.EvictedReleases, 2) // v1,v2 would be evicted
	assert.Equal(t, 2, rep.BlobsDeleted)  // would free
	assert.Greater(t, rep.ReclaimableBytes, int64(0))

	// Nothing actually changed.
	for _, k := range keys {
		ex, _ := store.Exists(ctx, k)
		assert.True(t, ex)
	}
	_, err = d.GetRelease(ctx, p.ID, "v1")
	assert.NoError(t, err)
}

func TestRun_AbandonedSweep(t *testing.T) {
	d, store, p := setup(t)
	ctx := context.Background()

	putRelease(t, d, store, p.ID, "v1", 1, "main", "published") // tip, kept

	key, size, err := store.Put(ctx, bytes.NewReader([]byte("abandoned")))
	require.NoError(t, err)
	unpub := &db.Release{ProjectID: p.ID, Version: "u1", VersionNum: 2, GitBranch: "main"}
	require.NoError(t, d.CreateRelease(ctx, unpub)) // never published
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: unpub.ID, OS: db.OSLinux, Arch: db.ArchAMD64, Kind: db.KindBinary,
		StorageKey: key, Size: size, SHA256: key,
	}))

	ret := New(d, store, Config{KeepN: 10, RecencyGuard: 24 * time.Hour, Enforce: true})
	ret.clock = futureClock()

	rep, err := ret.Run(ctx)
	require.NoError(t, err)
	assert.Len(t, rep.AbandonedReleases, 1)
	assert.Empty(t, rep.EvictedReleases) // only one published release, under keep-N

	ex, _ := store.Exists(ctx, key)
	assert.False(t, ex)
	_, err = d.GetRelease(ctx, p.ID, "u1")
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestRun_NothingToDo(t *testing.T) {
	d, store, p := setup(t)
	putRelease(t, d, store, p.ID, "v1", 1, "main", "only")

	ret := New(d, store, Config{KeepN: 10, RecencyGuard: 24 * time.Hour, Enforce: true})
	ret.clock = futureClock()

	rep, err := ret.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, rep.Releases())
	assert.Equal(t, 0, rep.BlobsDeleted)
}

func TestDeleteBlobIfUnreferenced(t *testing.T) {
	d, store, p := setup(t)
	ctx := context.Background()
	key := putRelease(t, d, store, p.ID, "v1", 1, "main", "data")

	// Referenced by the artifact -> kept.
	deleted, err := DeleteBlobIfUnreferenced(ctx, d, store, key, true)
	require.NoError(t, err)
	assert.False(t, deleted)
	ex, _ := store.Exists(ctx, key)
	assert.True(t, ex)

	// Unreferenced orphan, report-only -> would delete, but left in place.
	orphan, _, err := store.Put(ctx, bytes.NewReader([]byte("orphan")))
	require.NoError(t, err)
	would, err := DeleteBlobIfUnreferenced(ctx, d, store, orphan, false)
	require.NoError(t, err)
	assert.True(t, would)
	ex, _ = store.Exists(ctx, orphan)
	assert.True(t, ex)

	// Unreferenced orphan, enforce -> deleted.
	deleted, err = DeleteBlobIfUnreferenced(ctx, d, store, orphan, true)
	require.NoError(t, err)
	assert.True(t, deleted)
	ex, _ = store.Exists(ctx, orphan)
	assert.False(t, ex)

	// Empty key is a no-op.
	deleted, err = DeleteBlobIfUnreferenced(ctx, d, store, "", true)
	require.NoError(t, err)
	assert.False(t, deleted)
}
