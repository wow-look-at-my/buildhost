package npm

// A packument describes every published release of a project. Reflecting a
// pre-built package's manifest means reading package/package.json out of the

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

type countingStore struct {
	storage.Storage
	mu    sync.Mutex
	gets  int
	delay time.Duration
}

func (c *countingStore) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	c.mu.Lock()
	c.gets++
	c.mu.Unlock()
	if c.delay > 0 {
		select {
		case <-time.After(c.delay):
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}
	return c.Storage.Get(ctx, key)
}

func (c *countingStore) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gets
}

func withCountingStore(t *testing.T, delay time.Duration) *countingStore {
	t.Helper()
	_, store := routerEnv(t)
	c := &countingStore{Storage: store, delay: delay}
	prev := handler.Store
	handler.Store = c
	t.Cleanup(func() { handler.Store = prev })
	return c
}

func withFillBudget(t *testing.T, d time.Duration) {
	t.Helper()
	prev := handler.fillBudget
	handler.fillBudget = d
	t.Cleanup(func() { handler.fillBudget = prev })
}

// npmTarball builds a gzipped npm tarball carrying package/package.json with
// the given fields -- the shape the manifest-reflection path reads.
func npmTarball(t *testing.T, pkgJSON map[string]any) string {
	t.Helper()
	body, err := json.Marshal(pkgJSON)
	require.NoError(t, err)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o644, Size: int64(len(body))}))
	_, err = tw.Write(body)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.String()
}

func seedNPMPackageReleases(t *testing.T, project string, n int) []string {
	t.Helper()
	d, store := routerEnv(t)
	ctx := context.Background()
	proj := &db.Project{Name: project, Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	var versions []string
	for i := 1; i <= n; i++ {
		version := fmt.Sprintf("%d.0.0", i)
		rel := &db.Release{ProjectID: proj.ID, Version: version, VersionNum: int64(i) * 1000000}
		require.NoError(t, d.CreateRelease(ctx, rel))

		key, size, err := store.Put(ctx, strings.NewReader(npmTarball(t, map[string]any{
			"name":                 "@buildhost/" + project,
			"version":              version,
			"optionalDependencies": map[string]any{"@buildhost/" + project + "-linux-x64": version},
			"scripts":              map[string]any{"postinstall": "echo nope"},
		})))
		require.NoError(t, err)
		require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
			ReleaseID: rel.ID, OS: "any", Arch: "any",
			Kind: db.KindNPMPackage, StorageKey: key, Size: size, SHA256: key,
		}))
		require.NoError(t, d.PublishRelease(ctx, rel.ID))
		versions = append(versions, version)
	}
	return versions
}

// TestRouter_Packument_ManifestCacheBoundsBlobReads is the regression test for
// the registry hang: a warm packument must read NO blobs at all, however many
// releases the project has, and must serve the same manifest fields it served
// while cold.
func TestRouter_Packument_ManifestCacheBoundsBlobReads(t *testing.T) {
	t.Serial()
	const releases = 12
	versions := seedNPMPackageReleases(t, "packument-cache", releases)
	store := withCountingStore(t, 0)

	rec := npmGet(t, "", "/@buildhost/packument-cache")
	require.Equal(t, http.StatusOK, rec.Code)
	cold := decodePackument(t, rec)
	assert.Equal(t, releases, store.count(), "a cold packument extracts each release's manifest exactly once")

	coldVersions := cold["versions"].(map[string]any)
	require.Len(t, coldVersions, releases)
	for _, v := range versions {
		entry := coldVersions[v].(map[string]any)
		assert.Equal(t, map[string]any{"@buildhost/packument-cache-linux-x64": v},
			entry["optionalDependencies"], "version %s keeps its dependency graph", v)
	}

	before := store.count()
	rec = npmGet(t, "", "/@buildhost/packument-cache")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, before, store.count(), "a warm packument reads no blobs -- the work must not scale with the release count")
	assert.Equal(t, cold, decodePackument(t, rec), "the cached packument is byte-for-byte the one the blobs produced")
}

// TestRouter_Packument_SharedBlobExtractedOnce covers the other way a project
// accumulates releases cheaply: a re-release of unchanged bytes registers the
// SAME content-addressed blob (a hash-reference upload), so however many
func TestRouter_Packument_SharedBlobExtractedOnce(t *testing.T) {
	t.Serial()
	const releases = 6
	d, store := routerEnv(t)
	ctx := context.Background()
	proj := &db.Project{Name: "packument-sharedblob", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	key, size, err := store.Put(ctx, strings.NewReader(npmTarball(t, map[string]any{
		"dependencies": map[string]any{"left-pad": "^1.0.0"},
	})))
	require.NoError(t, err)
	for i := 1; i <= releases; i++ {
		rel := &db.Release{ProjectID: proj.ID, Version: fmt.Sprintf("%d.0.0", i), VersionNum: int64(i) * 1000000}
		require.NoError(t, d.CreateRelease(ctx, rel))
		require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
			ReleaseID: rel.ID, OS: "any", Arch: "any",
			Kind: db.KindNPMPackage, StorageKey: key, Size: size, SHA256: key,
		}))
		require.NoError(t, d.PublishRelease(ctx, rel.ID))
	}

	counter := withCountingStore(t, 0)
	rec := npmGet(t, "", "/@buildhost/packument-sharedblob")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, counter.count(), "one blob, one decompression -- not one per release")

	versions := decodePackument(t, rec)["versions"].(map[string]any)
	require.Len(t, versions, releases)
	for v, entry := range versions {
		assert.Equal(t, map[string]any{"left-pad": "^1.0.0"},
			entry.(map[string]any)["dependencies"], "version %s carries the shared manifest", v)
	}

	rec = npmGet(t, "", "/@buildhost/packument-sharedblob")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, counter.count(), "every sharing release got its own cache row")
}

// TestRouter_Packument_UnreadableBlobIsCached pins the other half of the
// bound: a blob that is not a readable npm tarball is a final answer about that
// artifact, so it must be cached too. Re-reading it on every request would keep
// the per-release blob read -- and the hang -- for exactly the packages whose
// manifests can never be read.
func TestRouter_Packument_UnreadableBlobIsCached(t *testing.T) {
	t.Serial()
	seedNPMPackage(t, "packument-unreadable", "1.0.0", "not a gzip tarball")
	store := withCountingStore(t, 0)

	rec := npmGet(t, "", "/@buildhost/packument-unreadable")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, store.count())
	entry := decodePackument(t, rec)["versions"].(map[string]any)["1.0.0"].(map[string]any)
	_, hasDeps := entry["optionalDependencies"]
	assert.False(t, hasDeps, "an unreadable blob still yields a minimal-but-valid entry")

	rec = npmGet(t, "", "/@buildhost/packument-unreadable")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, store.count(), "the unreadable verdict is cached, not re-derived per request")
}

func TestRouter_Packument_FillBudgetFailsLoudly(t *testing.T) {
	t.Serial()
	seedNPMPackageReleases(t, "packument-budget", 3)
	withCountingStore(t, 2*time.Second)
	withFillBudget(t, 20*time.Millisecond)

	start := time.Now()
	rec := npmGet(t, "", "/@buildhost/packument-budget")
	elapsed := time.Since(start)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code, "an unresolvable packument is an error, not a stripped 200")
	assert.Equal(t, "5", rec.Header().Get("Retry-After"))
	assert.Less(t, elapsed, 2*time.Second, "the budget bounds the request instead of letting it hang")
}

// TestRouter_Packument_BudgetFailureConverges proves the failure above is
// self-healing rather than permanent: whatever a budget-limited request managed
// to extract is committed, so a retry has less to do and the packument
// eventually serves.
func TestRouter_Packument_BudgetFailureConverges(t *testing.T) {
	t.Serial()
	const releases = 3
	seedNPMPackageReleases(t, "packument-converge", releases)
	store := withCountingStore(t, 0)
	withFillBudget(t, time.Nanosecond)

	require.Equal(t, http.StatusServiceUnavailable, npmGet(t, "", "/@buildhost/packument-converge").Code)

	// A budget that cannot expire mid-request resolves everything and caches it.
	handler.fillBudget = 30 * time.Second
	rec := npmGet(t, "", "/@buildhost/packument-converge")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, decodePackument(t, rec)["versions"], releases)

	before := store.count()
	require.Equal(t, http.StatusOK, npmGet(t, "", "/@buildhost/packument-converge").Code)
	assert.Equal(t, before, store.count(), "the recovering request filled the cache for good")
}
