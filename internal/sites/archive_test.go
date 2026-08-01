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
// the indexed read path from the tar scan it replaced: serving from an archive
// must never fall back to streaming the blob from the start.
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

// withCountingStore swaps the package handler's store for a counting one. The
// sites tests do not run in parallel, so the swap is safe.
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

	// A miss still finds the site's own 404 page -- which used to mean a SECOND
	// full scan of the archive.
	rec = env.do(t, "GET", "/indexed/nope.html", "", nil, false)
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "<h1>missing</h1>", rec.Body.String())

	assert.Equal(t, before, counting.count(),
		"serving from an archive must not stream the blob: %d sequential Get calls", counting.count()-before)
}

// TestLegacyTarSiteStillServes pins the fallback: every site uploaded before
// this format is a plain tar blob, and those must keep serving exactly as they
// did -- through the scan, since there is no index to use.
func TestLegacyTarSiteStillServes(t *testing.T) {
	env := setupEnv(t)
	proj := seedProject(t, env.db, "legacy")
	counting := withCountingStore(t, env.store)

	// Store a bare tar, the way uploads did before archives.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range map[string]string{"index.html": "<h1>old</h1>", "404.html": "<h1>old 404</h1>"} {
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
		ProjectID: proj.ID, Branch: "main", StorageKey: key, Size: size, FileCount: 2,
	})
	require.NoError(t, err)

	rec := env.do(t, "GET", "/legacy/", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "<h1>old</h1>", rec.Body.String())

	rec = env.do(t, "GET", "/legacy/gone.html", "", nil, false)
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "<h1>old 404</h1>", rec.Body.String())

	assert.Positive(t, counting.count(), "a legacy tar site is served by the sequential scan")
}
