package scratch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootHonoursTmpdir(t *testing.T) {
	t.Serial()
	t.Cleanup(func() { Configure("") })
	t.Setenv("TMPDIR", "/custom/tmpdir")
	Configure("")
	assert.Equal(t, "/custom/tmpdir", Root())
}

func TestRootOverrideWins(t *testing.T) {
	t.Serial()
	t.Cleanup(func() { Configure("") })
	t.Setenv("TMPDIR", "/custom/tmpdir")
	Configure("/var/scratch")
	assert.Equal(t, "/var/scratch", Root())
}

func TestDirCreatesTheSubdirectory(t *testing.T) {
	t.Serial()
	t.Cleanup(func() { Configure("") })
	Configure(t.TempDir())

	dir, err := Dir("buildhost-tools")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(Root(), "buildhost-tools"), dir)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func writeAt(t *testing.T, path string, when time.Time) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
	require.NoError(t, os.Chtimes(path, when, when))
}

// The sweep removes scratch this server creates and nothing else. An upload
// spool, a file with an unknown name and a file inside the stale age all
// survive.
func TestJanitorReclaimsOnlyOrphanedScratch(t *testing.T) {
	t.Serial()
	root := t.TempDir()
	legacy := t.TempDir()
	cache := t.TempDir()
	old := time.Now().Add(-2 * time.Hour)

	for _, name := range []string{"strip-input-abc", "stripped-abc", "debug-abc"} {
		writeAt(t, filepath.Join(root, name), old)
	}
	writeAt(t, filepath.Join(root, "recent-unknown"), time.Now())
	writeAt(t, filepath.Join(legacy, "strip-input-legacy"), old)
	writeAt(t, filepath.Join(legacy, "debug-legacy"), old)

	spoolDir := filepath.Join(legacy, "uploads")
	require.NoError(t, os.MkdirAll(spoolDir, 0o755))
	writeAt(t, filepath.Join(spoolDir, "session-1"), old)
	ociDir := filepath.Join(legacy, "oci-uploads")
	require.NoError(t, os.MkdirAll(ociDir, 0o755))
	writeAt(t, filepath.Join(ociDir, "upload-1"), old)

	enterprise := filepath.Join(cache, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	writeAt(t, enterprise, old)
	fresh := filepath.Join(cache, "fresh-entry")
	writeAt(t, fresh, time.Now())

	jan := &Janitor{Root: root, LegacyDir: legacy, CacheDir: cache, StaleAge: time.Hour}
	rep := jan.Run()
	assert.Equal(t, 6, rep.Files, "three in the root, two in the legacy dir, one cache entry")
	assert.Positive(t, rep.Bytes)

	for _, name := range []string{"strip-input-abc", "stripped-abc", "debug-abc"} {
		_, err := os.Stat(filepath.Join(root, name))
		assert.True(t, os.IsNotExist(err), "%s must be gone", name)
	}
	for _, name := range []string{"strip-input-legacy", "debug-legacy"} {
		_, err := os.Stat(filepath.Join(legacy, name))
		assert.True(t, os.IsNotExist(err), "%s must be gone", name)
	}
	_, err := os.Stat(filepath.Join(spoolDir, "session-1"))
	assert.NoError(t, err, "an upload session spool is durable")
	_, err = os.Stat(filepath.Join(ociDir, "upload-1"))
	assert.NoError(t, err, "an OCI upload spool is durable")
	_, err = os.Stat(filepath.Join(root, "recent-unknown"))
	assert.NoError(t, err, "a file inside the stale age is not orphaned")
	_, err = os.Stat(fresh)
	assert.NoError(t, err, "a cache entry inside the stale age is kept")
	_, err = os.Stat(enterprise)
	assert.True(t, os.IsNotExist(err), "an old cache entry is reclaimed")
}

// A goproxy source tree left by a crash is a whole directory of bytes.
func TestJanitorRemovesOrphanedScratchDirectories(t *testing.T) {
	t.Serial()
	root := t.TempDir()
	src := filepath.Join(root, "goproxy-src-123")
	require.NoError(t, os.MkdirAll(src, 0o755))
	writeAt(t, filepath.Join(src, "go.mod"), time.Now().Add(-3*time.Hour))
	require.NoError(t, os.Chtimes(src, time.Now().Add(-3*time.Hour), time.Now().Add(-3*time.Hour)))

	jan := &Janitor{Root: root, StaleAge: time.Hour}
	rep := jan.Run()
	assert.Equal(t, 1, rep.Dirs)
	_, err := os.Stat(src)
	assert.True(t, os.IsNotExist(err))
}

func TestJanitorToleratesMissingDirectories(t *testing.T) {
	t.Serial()
	jan := &Janitor{
		Root:      filepath.Join(t.TempDir(), "absent"),
		LegacyDir: filepath.Join(t.TempDir(), "absent"),
		CacheDir:  filepath.Join(t.TempDir(), "absent"),
		StaleAge:  time.Hour,
	}
	assert.Equal(t, SweepReport{}, jan.Run())
}
