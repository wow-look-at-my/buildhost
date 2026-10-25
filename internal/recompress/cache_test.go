package recompress

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insert(t *testing.T, c *Cache, key string, body string) int64 {
	t.Helper()
	f, err := c.Create()
	require.NoError(t, err)
	_, err = f.WriteString(body)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	size, err := c.Insert(key, f.Name())
	require.NoError(t, err)
	return size
}

func read(t *testing.T, c *Cache, key string) (string, bool) {
	t.Helper()
	f, size, ok := c.Open(key)
	if !ok {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	require.Equal(t, size, int64(len(data)))
	return string(data), true
}

func TestCacheRoundTrip(t *testing.T) {
	t.Serial()
	c, err := NewCache(t.TempDir(), 1<<20, 8, time.Hour)
	require.NoError(t, err)

	key := Key("blob-1", "t1", "br", false)
	assert.Equal(t, int64(5), insert(t, c, key, "hello"))

	body, ok := read(t, c, key)
	require.True(t, ok)
	assert.Equal(t, "hello", body)
}

func TestCacheMissForUnknownKey(t *testing.T) {
	t.Serial()
	c, err := NewCache(t.TempDir(), 1<<20, 8, time.Hour)
	require.NoError(t, err)

	_, ok := read(t, c, Key("blob-1", "t1", "br", false))
	assert.False(t, ok)
}

// A key covers every input that changes the bytes, so two variants can never
// be served for each other.
func TestCacheKeyCoversEveryInput(t *testing.T) {
	t.Serial()
	base := Key("blob-1", "t1", "br", false)
	assert.NotEqual(t, base, Key("blob-2", "t1", "br", false))
	assert.Equal(t, base, Key("blob-1", "t1", "br", false))
	assert.NotEqual(t, base, Key("blob-1", "t2", "br", false))
	assert.NotEqual(t, base, Key("blob-1", "t1", "gzip", false))
	assert.NotEqual(t, base, Key("blob-1", "t1", "br", true))
	assert.NotEqual(t, base, Key("blob-1", "t1", "identity", false))
	assert.Len(t, base, 64)
}

func TestCacheExpiresAfterTTL(t *testing.T) {
	t.Serial()
	c, err := NewCache(t.TempDir(), 1<<20, 8, time.Hour)
	require.NoError(t, err)

	key := Key("blob-1", "t1", "br", false)
	insert(t, c, key, "hello")
	// Reading inside the TTL does not extend it: a cached body has one age.
	c.now = func() time.Time { return time.Now().Add(2 * time.Hour) }

	_, ok := read(t, c, key)
	assert.False(t, ok, "an entry older than the TTL must not be served")
	assert.Zero(t, c.Len())
	_, err = os.Stat(filepath.Join(c.Dir(), key))
	assert.True(t, os.IsNotExist(err), "an expired entry is removed from disk")
}

// The count bound drops the entry touched longest ago, which is not the one
// created first.
func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	t.Serial()
	c, err := NewCache(t.TempDir(), 1<<20, 2, time.Hour)
	require.NoError(t, err)

	a := Key("a", "t1", "br", false)
	b := Key("b", "t1", "br", false)
	newKey := Key("c", "t1", "br", false)
	insert(t, c, a, "aaa")
	insert(t, c, b, "bbb")
	// b is the newest, a the oldest; read a so b becomes the victim.
	_, ok := read(t, c, a)
	require.True(t, ok)
	insert(t, c, newKey, "ccc")

	assert.Equal(t, 2, c.Len())
	if _, ok := read(t, c, b); ok {
		t.Fatal("the least recently used entry survived")
	}
	if body, ok := read(t, c, a); !ok || body != "aaa" {
		t.Fatal("the recently read entry was evicted")
	}
	if body, ok := read(t, c, newKey); !ok || body != "ccc" {
		t.Fatal("the newly inserted entry is missing")
	}
}

// The byte bound evicts the same way, and the newest entry is never the
// victim of its own insert.
func TestCacheEvictsBySize(t *testing.T) {
	t.Serial()
	c, err := NewCache(t.TempDir(), 10, 100, time.Hour)
	require.NoError(t, err)

	a := Key("a", "t1", "br", false)
	insert(t, c, a, "01234567")
	_, ok := read(t, c, a)
	require.True(t, ok)
	b := Key("b", "t1", "br", false)
	insert(t, c, b, "01234567")

	assert.Equal(t, 1, c.Len())
	assert.Equal(t, int64(8), c.Bytes())
	if _, ok := read(t, c, a); ok {
		t.Fatal("the older entry survived a size bound")
	}
}

// A variant larger than the whole cache is not stored, so one big response
// cannot flush every other entry.
func TestCacheRefusesOversizedEntry(t *testing.T) {
	t.Serial()
	c, err := NewCache(t.TempDir(), 10, 8, time.Hour)
	require.NoError(t, err)

	size, err := func() (int64, error) {
		f, err := c.Create()
		require.NoError(t, err)
		_, err = f.WriteString("01234567890123456789")
		require.NoError(t, err)
		require.NoError(t, f.Close())
		return c.Insert(Key("big", "t1", "br", false), f.Name())
	}()
	require.NoError(t, err)
	assert.Equal(t, int64(20), size)
	assert.Zero(t, c.Len())
}

func TestCacheLoadsExistingEntries(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	c, err := NewCache(dir, 1<<20, 8, time.Hour)
	require.NoError(t, err)
	key := Key("blob-1", "t1", "br", false)
	insert(t, c, key, "hello")

	reopened, err := NewCache(dir, 1<<20, 8, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, reopened.Len())
	if body, ok := read(t, reopened, key); !ok || body != "hello" {
		t.Fatal("an entry on disk was not indexed")
	}
}

// A file whose name is not a key is not an entry, so a stray temporary file
// is never served.
func TestCacheIgnoresForeignFiles(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "incoming-123"), []byte("x"), 0o600))
	c, err := NewCache(dir, 1<<20, 8, time.Hour)
	require.NoError(t, err)
	assert.Zero(t, c.Len())
}
