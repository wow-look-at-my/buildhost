package sites

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestListFilesRefusesTarSite(t *testing.T) {
	t.Serial()
	env := setupEnv(t)
	proj := seedProject(t, env.db, "tarlist")
	key := seedTarSite(t, env, proj, "main", map[string]string{"z.html": "zz"})

	_, err := ListFiles(context.Background(), env.store, key)
	require.Error(t, err, "the listing reads archives only")
}
