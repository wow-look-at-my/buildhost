package sites

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/binarchive"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// countingStore records sequential Get calls, which is how these tests tell
type countingStore struct {
	storage.Storage
	mu   sync.Mutex
	gets int
}

func (c *countingStore) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	c.mu.Lock()
	c.gets++
	c.mu.Unlock()
	return c.Storage.Get(ctx, key)
}

func (c *countingStore) OpenReaderAt(ctx context.Context, key string) (storage.ReaderAtCloser, int64, error) {
	rg, ok := c.Storage.(storage.RandomGetter)
	if !ok {
		return nil, 0, storage.ErrRandomUnsupported
	}
	return rg.OpenReaderAt(ctx, key)
}

func (c *countingStore) PutUncompressed(ctx context.Context, r io.Reader) (string, int64, error) {
	up, ok := c.Storage.(storage.UncompressedPutter)
	if !ok {
		return c.Storage.Put(ctx, r)
	}
	return up.PutUncompressed(ctx, r)
}

func (c *countingStore) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gets
}

func withCountingStore(t *testing.T, inner storage.Storage) *countingStore {
	t.Helper()
	c := &countingStore{Storage: inner}
	prev := handler.Store
	handler.Store = c
	t.Cleanup(func() { handler.Store = prev })
	return c
}

// TestUploadStoresArchiveAndServesByIndex is the end-to-end proof of the
// format change: an uploaded site is stored as a binpazer archive, and serving
// a file out of it never streams the blob from the start.
func TestUploadStoresArchiveAndServesByIndex(t *testing.T) {
	t.Serial()
	env := setupEnv(t)
	proj := seedProject(t, env.db, "indexed")
	counting := withCountingStore(t, env.store)

	files := map[string]string{
		"index.html": "<h1>home</h1>",
		"404.html":   "<h1>missing</h1>",
		"a/b/c.css":  "body{color:red}",
	}
	for i := 0; i < 40; i++ {
		files[fmt.Sprintf("filler/%02d.txt", i)] = strings.Repeat("filler ", 500)
	}
	env.uploadSite(t, "indexed", "main", files)

	// What got stored is an archive, not the tar it used to be.
	site, err := env.db.GetSite(context.Background(), proj.ID, "main")
	require.NoError(t, err)
	rc, _, err := env.store.Get(context.Background(), site.StorageKey)
	require.NoError(t, err)
	head := make([]byte, len(binarchive.Magic))
	_, err = io.ReadFull(rc, head)
	require.NoError(t, err)
	rc.Close()
	assert.True(t, binarchive.IsArchive(head), "the stored blob must be a binpazer archive")

	before := counting.count()

	rec := env.do(t, "GET", "/indexed/a/b/c.css", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "body{color:red}", rec.Body.String())

	rec = env.do(t, "GET", "/indexed/", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "<h1>home</h1>", rec.Body.String())

	rec = env.do(t, "GET", "/indexed/nope.html", "", nil, false)
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "<h1>missing</h1>", rec.Body.String())

	assert.Equal(t, before, counting.count(),
		"serving from an archive must not stream the blob: %d sequential Get calls", counting.count()-before)
}

// seedTarSite stores a site the way uploads did before archives: a plain tar,
// zstd-compressed by the store.
func seedTarSite(t *testing.T, env *testEnv, proj *db.Project, branch string, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())

	key, size, err := env.store.Put(context.Background(), bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	_, err = env.db.UpsertSite(context.Background(), &db.Site{
		ProjectID: proj.ID, Branch: branch, StorageKey: key, Size: size, FileCount: int64(len(files)),
	})
	require.NoError(t, err)
	return key
}

func TestTarSiteIsNotServed(t *testing.T) {
	t.Serial()
	env := setupEnv(t)
	proj := seedProject(t, env.db, "tarsite")
	seedTarSite(t, env, proj, "main", map[string]string{"index.html": "<h1>old</h1>"})

	rec := env.do(t, "GET", "/tarsite/", "", nil, false)
	assert.Equal(t, http.StatusInternalServerError, rec.Code, "the serve path reads archives only")
}

func TestConvertTarSites(t *testing.T) {
	t.Serial()
	env := setupEnv(t)
	ctx := context.Background()
	proj := seedProject(t, env.db, "converted")
	oldKey := seedTarSite(t, env, proj, "main", map[string]string{
		"index.html": "<h1>old</h1>",
		"404.html":   "<h1>old 404</h1>",
		"a/b.css":    "body{}",
	})
	env.uploadSite(t, "converted", "fresh", map[string]string{"index.html": "new"})
	fresh, err := env.db.GetSite(ctx, proj.ID, "fresh")
	require.NoError(t, err)

	require.NoError(t, ConvertTarSites(ctx, env.db, env.store, t.TempDir()))

	site, err := env.db.GetSite(ctx, proj.ID, "main")
	require.NoError(t, err)
	require.NotEqual(t, oldKey, site.StorageKey, "the row must point at the new archive")
	_, _, err = env.store.Get(ctx, oldKey)
	assert.Error(t, err, "the tar blob must be deleted once nothing references it")

	counting := withCountingStore(t, env.store)
	rec := env.do(t, "GET", "/converted/a/b.css", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "body{}", rec.Body.String())
	rec = env.do(t, "GET", "/converted/gone.html", "", nil, false)
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "<h1>old 404</h1>", rec.Body.String())
	assert.Zero(t, counting.count(), "a converted site must be served through its index")

	again, err := env.db.GetSite(ctx, proj.ID, "fresh")
	require.NoError(t, err)
	assert.Equal(t, fresh.StorageKey, again.StorageKey, "an archive site must be left alone")

	require.NoError(t, ConvertTarSites(ctx, env.db, env.store, t.TempDir()))
	after, err := env.db.GetSite(ctx, proj.ID, "main")
	require.NoError(t, err)
	assert.Equal(t, site.StorageKey, after.StorageKey, "a second run must change nothing")
}

func TestConvertTarSitesReportsFailures(t *testing.T) {
	t.Serial()
	env := setupEnv(t)
	ctx := context.Background()
	proj := seedProject(t, env.db, "broken")
	key, size, err := env.store.Put(ctx, bytes.NewReader([]byte("not a tar at all, and long enough to fail")))
	require.NoError(t, err)
	_, err = env.db.UpsertSite(ctx, &db.Site{ProjectID: proj.ID, Branch: "main", StorageKey: key, Size: size})
	require.NoError(t, err)

	err = ConvertTarSites(ctx, env.db, env.store, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "main")
}
