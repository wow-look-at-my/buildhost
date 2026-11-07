package cicache

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/diskusage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var scope = db.CacheScope{RepoID: "222", Ref: "refs/heads/master"}

type fixture struct {
	j *Janitor
	// free is the percent of disk free.
	free uint64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	store, err := storage.NewFilesystem(t.TempDir(), false)
	require.NoError(t, err)
	f := &fixture{free: 50}
	f.j = &Janitor{
		DB: d, Store: store, DataDir: t.TempDir(), Keep: 7 * 24 * time.Hour, MinFreePercent: 15,
		Disk: func(string) (diskusage.Usage, error) {
			return diskusage.Usage{Total: 100, Free: f.free, Used: 100 - f.free}, nil
		},
	}
	return f
}

// save stores body under key, last used usedAgo before now, the way the PUT handler does.
func (f *fixture) save(t *testing.T, key, body string, usedAgo time.Duration) string {
	t.Helper()
	ctx := t.Context()
	storageKey, size, err := f.j.Store.Put(ctx, strings.NewReader(body))
	require.NoError(t, err)
	inserted, err := f.j.DB.PutCache(ctx, scope, key, "wow-look-at-my/consumer", db.RunBlob{StorageKey: storageKey, Size: size})
	require.NoError(t, err)
	require.True(t, inserted)
	_, err = f.j.DB.Exec(`UPDATE cache_entries SET used_at = datetime('now', ?) WHERE key = ?`, fmt.Sprintf("-%d seconds", int(usedAgo.Seconds())), key)
	require.NoError(t, err)
	return storageKey
}

func (f *fixture) holds(t *testing.T, key string) bool {
	t.Helper()
	_, found, err := f.j.DB.GetCache(t.Context(), scope, key)
	require.NoError(t, err)
	return found
}

func TestSweep_DeletesAnEntryNothingRestoredForKeep(t *testing.T) {
	t.Serial()
	f := newFixture(t)
	stale := f.save(t, "stale", "old bytes", 8*24*time.Hour)
	f.save(t, "fresh", "new bytes", time.Hour)

	require.NoError(t, f.j.Sweep(t.Context(), time.Now()))
	assert.False(t, f.holds(t, "stale"), "an entry unused past Keep goes")
	assert.True(t, f.holds(t, "fresh"))
	_, _, err := f.j.Store.Get(t.Context(), stale)
	assert.Error(t, err, "the blob nothing else references goes with it")
	rep := LastReport()
	require.NotNil(t, rep)
	assert.Equal(t, 1, rep.Expired)
	assert.Equal(t, 2, rep.Entries)
}

func TestSweep_DiskPressureDeletesLeastRecentlyUsedFirst(t *testing.T) {
	t.Serial()
	f := newFixture(t)
	keys := []string{"oldest", "middle", "newest"}
	f.save(t, "oldest", "a", PressureGrace+3*time.Hour)
	f.save(t, "middle", "b", PressureGrace+2*time.Hour)
	f.save(t, "newest", "c", PressureGrace+time.Hour)
	f.j.Disk = func(string) (diskusage.Usage, error) {
		var free uint64 = 10
		for _, k := range keys {
			if !f.holds(t, k) {
				free += 4
			}
		}
		return diskusage.Usage{Total: 100, Free: free, Used: 100 - free}, nil
	}

	require.NoError(t, f.j.Sweep(t.Context(), time.Now()))
	assert.False(t, f.holds(t, "oldest"), "the least recently used goes first")
	assert.False(t, f.holds(t, "middle"))
	assert.True(t, f.holds(t, "newest"), "the deletions stop once free disk is back above the floor")
	assert.Equal(t, 2, LastReport().PressureDeleted)
}

// A disk that the cache does not fill stays below the floor whatever the
// janitor deletes. The pass must still keep the entries that a live run saved
// or restored, and say that it kept them.
func TestSweep_PressureTheCacheCannotRelieveKeepsRecentEntries(t *testing.T) {
	t.Serial()
	f := newFixture(t)
	f.free = 5
	f.save(t, "idle", "a", PressureGrace+time.Hour)
	f.save(t, "restored", "b", time.Minute)
	f.save(t, "just-saved", "c", 0)

	require.NoError(t, f.j.Sweep(t.Context(), time.Now()))
	assert.False(t, f.holds(t, "idle"), "an entry idle past the grace still goes for disk pressure")
	assert.True(t, f.holds(t, "restored"), "an entry restored a minute ago stays: the run that restored it can restore it again")
	assert.True(t, f.holds(t, "just-saved"), "an entry saved by an earlier job of a live run stays")
	rep := LastReport()
	assert.Equal(t, 1, rep.PressureDeleted)
	assert.Equal(t, 2, rep.PressureKept)
}

func TestSweep_NoPressureDeletesNothingRecent(t *testing.T) {
	t.Serial()
	f := newFixture(t)
	f.save(t, "recent", "x", time.Hour)
	require.NoError(t, f.j.Sweep(t.Context(), time.Now()))
	assert.True(t, f.holds(t, "recent"))
	assert.Equal(t, 0, LastReport().PressureDeleted)
}
