package uploads

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testStore(t *testing.T, maxSize int64, ttl time.Duration) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "uploads"), maxSize, ttl)
	require.NoError(t, err)
	return s
}

func TestStoreCreateAppendFinalize(t *testing.T) {
	s := testStore(t, 1<<20, time.Hour)

	sess, err := s.Create("owner-a")
	require.NoError(t, err)
	assert.Len(t, sess.ID(), 32, "128-bit hex id")
	assert.Equal(t, int64(0), sess.Size())

	// Sequential appends at the right offsets.
	size, err := s.Append(sess, 0, strings.NewReader("hello "))
	require.NoError(t, err)
	assert.Equal(t, int64(6), size)
	size, err = s.Append(sess, 6, strings.NewReader("world"))
	require.NoError(t, err)
	assert.Equal(t, int64(11), size)
	assert.Equal(t, int64(11), sess.Size())

	// Finalize sees the assembled bytes.
	f, n, err := s.BeginFinalize(sess)
	require.NoError(t, err)
	assert.Equal(t, int64(11), n)
	b, err := io.ReadAll(f)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(b))
	require.NoError(t, f.Close())

	// Consumed: the session and its spool are gone.
	path := sess.path
	s.Remove(sess)
	_, err = s.Get(sess.ID(), "owner-a")
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}

func TestStoreOffsetMismatch(t *testing.T) {
	s := testStore(t, 1<<20, time.Hour)
	sess, err := s.Create("o")
	require.NoError(t, err)

	_, err = s.Append(sess, 0, strings.NewReader("abcd"))
	require.NoError(t, err)

	// Wrong offset: rejected, size reported, nothing written.
	size, err := s.Append(sess, 2, strings.NewReader("xy"))
	assert.ErrorIs(t, err, ErrOffsetMismatch)
	assert.Equal(t, int64(4), size)

	// Resume from the reported size works.
	size, err = s.Append(sess, 4, strings.NewReader("ef"))
	require.NoError(t, err)
	assert.Equal(t, int64(6), size)
}

func TestStoreOwnerIsolation(t *testing.T) {
	s := testStore(t, 1<<20, time.Hour)
	sess, err := s.Create("owner-a")
	require.NoError(t, err)

	// Another identity cannot see the session at all.
	_, err = s.Get(sess.ID(), "owner-b")
	assert.ErrorIs(t, err, ErrNotFound)

	got, err := s.Get(sess.ID(), "owner-a")
	require.NoError(t, err)
	assert.Same(t, sess, got)
}

func TestStoreTooLargeRollsBack(t *testing.T) {
	s := testStore(t, 10, time.Hour)
	sess, err := s.Create("o")
	require.NoError(t, err)

	_, err = s.Append(sess, 0, strings.NewReader("12345"))
	require.NoError(t, err)

	size, err := s.Append(sess, 5, strings.NewReader("6789012345"))
	assert.ErrorIs(t, err, ErrTooLarge)
	assert.Equal(t, int64(5), size, "over-cap chunk rolled back entirely")

	// The session is still usable up to the cap.
	size, err = s.Append(sess, 5, strings.NewReader("67890"))
	require.NoError(t, err)
	assert.Equal(t, int64(10), size)

	f, n, err := s.BeginFinalize(sess)
	require.NoError(t, err)
	defer f.Close()
	assert.Equal(t, int64(10), n)
	b, _ := io.ReadAll(f)
	assert.Equal(t, "1234567890", string(b))
}

func TestStoreBusyDuringFinalize(t *testing.T) {
	s := testStore(t, 1<<20, time.Hour)
	sess, err := s.Create("o")
	require.NoError(t, err)
	_, err = s.Append(sess, 0, strings.NewReader("data"))
	require.NoError(t, err)

	f, _, err := s.BeginFinalize(sess)
	require.NoError(t, err)
	defer f.Close()

	_, err = s.Append(sess, 4, strings.NewReader("more"))
	assert.ErrorIs(t, err, ErrBusy)
	_, _, err = s.BeginFinalize(sess)
	assert.ErrorIs(t, err, ErrBusy)

	// A failed finalize releases the session for another try.
	s.EndFinalize(sess)
	_, err = s.Append(sess, 4, strings.NewReader("more"))
	require.NoError(t, err)
	assert.Equal(t, int64(8), sess.Size())
}

func TestStorePartialAppendCommits(t *testing.T) {
	s := testStore(t, 1<<20, time.Hour)
	sess, err := s.Create("o")
	require.NoError(t, err)

	// A reader that delivers some bytes and then fails, like a dropped
	failing := io.MultiReader(strings.NewReader("part"), errReader{})
	size, err := s.Append(sess, 0, failing)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrTooLarge)
	assert.Equal(t, int64(4), size)
	assert.Equal(t, int64(4), sess.Size())

	size, err = s.Append(sess, 4, strings.NewReader("ial"))
	require.NoError(t, err)
	assert.Equal(t, int64(7), size)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection dropped") }

func TestStoreSweepExpired(t *testing.T) {
	s := testStore(t, 1<<20, time.Millisecond)
	old, err := s.Create("o")
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)

	// A busy session survives the sweep even when expired.
	f, _, err := s.BeginFinalize(old)
	require.NoError(t, err)
	s.SweepExpired()
	_, err = s.Get(old.ID(), "o")
	require.NoError(t, err, "busy session must not be swept")
	require.NoError(t, f.Close())
	s.EndFinalize(old)

	// Idle and expired: swept (Create sweeps opportunistically too).
	s.SweepExpired()
	_, err = s.Get(old.ID(), "o")
	assert.ErrorIs(t, err, ErrNotFound)
	_, statErr := os.Stat(old.path)
	assert.True(t, os.IsNotExist(statErr), "spool file removed by sweep")
}

func TestNewStoreClearsOrphanedSpools(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	orphan := filepath.Join(dir, "deadbeef.spool")
	require.NoError(t, os.WriteFile(orphan, []byte("leftover"), 0o600))

	_, err := NewStore(dir, 1<<20, time.Hour)
	require.NoError(t, err)
	_, statErr := os.Stat(orphan)
	assert.True(t, os.IsNotExist(statErr), "crashed-process leftovers cleared at startup")
}
