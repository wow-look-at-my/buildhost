package admin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestAdminBackup_DownloadsARestorableDatabase(t *testing.T) {
	t.Serial()
	srv, database, dir := newMergeServer(t)
	mergeTestProject(t, database, "backme", "wow-look-at-my/backme", mergeTestRepoID)

	rec := serve(srv, "GET", "/api/backup", nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment;")
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	restored := filepath.Join(t.TempDir(), "restored.db")
	require.NoError(t, os.WriteFile(restored, rec.Body.Bytes(), 0o600))
	got, err := db.Open(restored)
	require.NoError(t, err)
	defer got.Close()
	p, err := got.GetProject(context.Background(), "backme")
	require.NoError(t, err)
	assert.Equal(t, "backme", p.Name)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), "backup-", "the snapshot must not outlive the request")
	}
}
