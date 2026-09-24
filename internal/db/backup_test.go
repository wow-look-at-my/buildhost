package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackup_CopiesLiveDatabase(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	src := filepath.Join(dir, "live.db")
	live, err := Open(src)
	require.NoError(t, err)
	defer live.Close()
	ctx := context.Background()
	mergeProject(t, live, "backme", "wow-look-at-my/backme", testRepoID)

	dst := filepath.Join(dir, "backup.db")
	require.NoError(t, Backup(ctx, src, dst))

	restored, err := Open(dst)
	require.NoError(t, err)
	defer restored.Close()
	got, err := restored.GetProject(ctx, "backme")
	require.NoError(t, err)
	assert.Equal(t, "backme", got.Name)
}

func TestBackup_RefusesToOverwrite(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	src := filepath.Join(dir, "live.db")
	live, err := Open(src)
	require.NoError(t, err)
	defer live.Close()

	dst := filepath.Join(dir, "backup.db")
	require.NoError(t, os.WriteFile(dst, []byte("keep me"), 0o600))
	require.Error(t, Backup(context.Background(), src, dst))

	kept, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "keep me", string(kept), "a backup must never clobber an existing file")
}

// Read-only open must not create an empty database for a mistyped path and
// then report a successful backup of nothing.
func TestBackup_MissingSourceFails(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	src := filepath.Join(dir, "missing.db")
	require.Error(t, Backup(context.Background(), src, filepath.Join(dir, "backup.db")))
	_, err := os.Stat(src)
	assert.True(t, os.IsNotExist(err), "the source path must stay absent")
}
