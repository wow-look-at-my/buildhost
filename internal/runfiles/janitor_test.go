package runfiles

import (
	"context"
	"errors"
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

type fixture struct {
	j     *Janitor
	ended map[string]time.Time
	fail  map[string]bool
	free  uint64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	store, err := storage.NewFilesystem(t.TempDir(), false)
	require.NoError(t, err)
	f := &fixture{ended: map[string]time.Time{}, fail: map[string]bool{}, free: 50}
	f.j = &Janitor{
		DB: d, Store: store, DataDir: t.TempDir(), Keep: 24 * time.Hour, MinFreePercent: 10,
		RunEnded: func(_ context.Context, _, runID string) (time.Time, bool, error) {
			if f.fail[runID] {
				return time.Time{}, false, errors.New("GitHub answered 502")
			}
			at, ok := f.ended[runID]
			return at, ok, nil
		},
		Disk: func(string) (diskusage.Usage, error) {
			return diskusage.Usage{Total: 100, Free: f.free, Used: 100 - f.free}, nil
		},
	}
	return f
}

// send stores body as the run's file, the way the PUT handler does.
func (f *fixture) send(t *testing.T, runID, body string) string {
	t.Helper()
	ctx := t.Context()
	key := db.RunKey{RepoID: "222", RunID: runID}
	require.NoError(t, f.j.DB.TouchRun(ctx, key, "wow-look-at-my/consumer"))
	storageKey, size, err := f.j.Store.Put(ctx, strings.NewReader(body))
	require.NoError(t, err)
	_, err = f.j.DB.PutRunFile(ctx, key, "toolchain", db.RunBlob{StorageKey: storageKey, Size: size})
	require.NoError(t, err)
	return storageKey
}

func (f *fixture) holds(t *testing.T, runID string) bool {
	t.Helper()
	_, found, err := f.j.DB.GetRunFile(t.Context(), db.RunKey{RepoID: "222", RunID: runID}, "toolchain")
	require.NoError(t, err)
	return found
}

func TestSweep_KeepsALiveRun(t *testing.T) {
	t.Serial()
	f := newFixture(t)
	f.send(t, "1", "a")
	require.NoError(t, f.j.Sweep(t.Context(), time.Now().Add(48*time.Hour)))
	assert.True(t, f.holds(t, "1"), "a run GitHub reports live keeps its files")
}

func TestSweep_DeletesKeepAfterTheRunEnds(t *testing.T) {
	t.Serial()
	f := newFixture(t)
	key := f.send(t, "1", "a")
	now := time.Now()
	f.ended["1"] = now

	require.NoError(t, f.j.Sweep(t.Context(), now.Add(time.Hour)))
	assert.True(t, f.holds(t, "1"), "an ended run keeps its files for Keep")

	require.NoError(t, f.j.Sweep(t.Context(), now.Add(25*time.Hour)))
	assert.False(t, f.holds(t, "1"), "an ended run loses its files after Keep")
	rep := LastReport()
	require.NotNil(t, rep)
	assert.Equal(t, 1, rep.Deleted)
	assert.Equal(t, int64(1), rep.FreedBytes)
	exists, err := f.j.Store.Exists(t.Context(), key)
	require.NoError(t, err)
	assert.False(t, exists, "the blob goes with the run")
}

func TestSweep_UnknownStatusKeepsTheRun(t *testing.T) {
	t.Serial()
	f := newFixture(t)
	f.send(t, "1", "a")
	f.ended["1"] = time.Now().Add(-72 * time.Hour)
	f.fail["1"] = true
	require.NoError(t, f.j.Sweep(t.Context(), time.Now()))
	assert.True(t, f.holds(t, "1"), "a status GitHub did not answer is not an end")
	rep := LastReport()
	require.NotNil(t, rep)
	assert.Equal(t, 1, rep.CheckFailures)
	assert.Contains(t, rep.LastCheckError, "502")
}

func TestSweep_PressureDeletesEndedRunsOldestFirst(t *testing.T) {
	t.Serial()
	f := newFixture(t)
	f.send(t, "old", "a")
	f.send(t, "new", "b")
	f.send(t, "live", "c")
	now := time.Now()
	f.ended["old"] = now.Add(-2 * time.Hour)
	f.ended["new"] = now.Add(-time.Hour)
	f.free = 5
	f.j.Disk = func(string) (diskusage.Usage, error) {
		free := f.free
		f.free += 10
		return diskusage.Usage{Total: 100, Free: free, Used: 100 - free}, nil
	}

	require.NoError(t, f.j.Sweep(t.Context(), now))
	assert.False(t, f.holds(t, "old"), "the oldest ended run goes first under pressure")
	assert.True(t, f.holds(t, "new"), "the sweep stops once free disk is back above the floor")
	assert.True(t, f.holds(t, "live"), "a live run never goes early")
}

func TestSweep_PressureNeverTakesALiveRun(t *testing.T) {
	t.Serial()
	f := newFixture(t)
	f.send(t, "live", "c")
	f.free = 1
	require.NoError(t, f.j.Sweep(t.Context(), time.Now()))
	assert.True(t, f.holds(t, "live"))
}

func TestSweep_SharedBlobSurvives(t *testing.T) {
	t.Serial()
	f := newFixture(t)
	key := f.send(t, "1", "same")
	f.send(t, "2", "same")
	now := time.Now()
	f.ended["1"] = now.Add(-48 * time.Hour)

	require.NoError(t, f.j.Sweep(t.Context(), now))
	assert.False(t, f.holds(t, "1"))
	exists, err := f.j.Store.Exists(t.Context(), key)
	require.NoError(t, err)
	assert.True(t, exists, "a blob another run still holds must stay")
}

func TestSweep_ReRunRevivesAnEndedRun(t *testing.T) {
	t.Serial()
	f := newFixture(t)
	f.send(t, "1", "a")
	now := time.Now()
	f.ended["1"] = now.Add(-time.Hour)
	require.NoError(t, f.j.Sweep(t.Context(), now))

	delete(f.ended, "1")
	f.send(t, "1", "b")
	require.NoError(t, f.j.Sweep(t.Context(), now.Add(48*time.Hour)))
	assert.True(t, f.holds(t, "1"), "a re-run makes the run live again")
}
