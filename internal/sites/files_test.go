package sites

import (
	"archive/tar"
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestListFilesArchiveSite(t *testing.T) {
	t.Serial()
	env := setupEnv(t)
	proj := seedProject(t, env.db, "listed")
	env.uploadSite(t, "listed", "main", map[string]string{
		"profile.json": `{"a":1}`,
		"b/c/d.txt":    "four",
		"a.txt":        "x",
	})

	site, err := env.db.GetSite(context.Background(), proj.ID, "main")
	require.NoError(t, err)
	files, err := ListFiles(context.Background(), env.store, site.StorageKey)
	require.NoError(t, err)
	assert.Equal(t, []File{
		{Path: "a.txt", Size: 1},
		{Path: "b/c/d.txt", Size: 4},
		{Path: "profile.json", Size: 7},
	}, files)
}

func TestListFilesLegacyTarSite(t *testing.T) {
	t.Serial()
	env := setupEnv(t)
	proj := seedProject(t, env.db, "legacylist")

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "dir/", Mode: 0o755, Typeflag: tar.TypeDir}))
	for _, f := range []struct{ name, body string }{{"z.html", "zz"}, {"./dir/x.css", "xxx"}} {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: f.name, Mode: 0o644, Size: int64(len(f.body)), Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte(f.body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())

	key, size, err := env.store.Put(context.Background(), bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	_, err = env.db.UpsertSite(context.Background(), &db.Site{
		ProjectID: proj.ID, Branch: "main", StorageKey: key, Size: size, FileCount: 2,
	})
	require.NoError(t, err)

	files, err := ListFiles(context.Background(), env.store, key)
	require.NoError(t, err)
	assert.Equal(t, []File{{Path: "dir/x.css", Size: 3}, {Path: "z.html", Size: 2}}, files,
		"the listing must skip directory entries and clean each path the way the serve path does")
}
