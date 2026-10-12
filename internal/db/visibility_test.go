package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustProject(t *testing.T, d *DB, name string, private bool) *Project {
	t.Helper()
	p := &Project{Name: name, IsPrivate: private, Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(context.Background(), p))
	return p
}

func reload(t *testing.T, d *DB, name string) *Project {
	t.Helper()
	p, err := d.GetProject(context.Background(), name)
	require.NoError(t, err)
	return p
}

func TestParseVisibility(t *testing.T) {
	t.Serial()
	for _, s := range []string{"auto", "public", "private"} {
		v, err := ParseVisibility(s)
		require.NoError(t, err)
		assert.Equal(t, Visibility(s), v)
	}
	_, err := ParseVisibility("")
	assert.Error(t, err, "an empty mode is a mistake, not auto")
	_, err = ParseVisibility("hidden")
	assert.Error(t, err)
}

func TestFolderCovers(t *testing.T) {
	t.Serial()
	assert.True(t, FolderCovers("org", "org"))
	assert.True(t, FolderCovers("org", "org/repo"))
	assert.True(t, FolderCovers("org", "org/repo/child"))
	assert.False(t, FolderCovers("org", "organisation"), "a prefix must end at a slash")
	assert.False(t, FolderCovers("org/repo", "org"))
}

// The OIDC sync records the repo visibility as the auto value. It must never
// overwrite an operator override.
func TestSyncAutoPrivate_OverrideWins(t *testing.T) {
	t.Serial()
	ctx := context.Background()
	d := openTestDB(t)
	p := mustProject(t, d, "org/app", true)

	require.NoError(t, d.SetProjectVisibilityMode(ctx, p, VisibilityPublic))
	assert.False(t, reload(t, d, "org/app").IsPrivate)

	require.NoError(t, d.SyncAutoPrivate(ctx, p, true))
	assert.False(t, reload(t, d, "org/app").IsPrivate, "the repo is private, but the project is forced public")

	require.NoError(t, d.SetProjectVisibilityMode(ctx, p, VisibilityAuto))
	assert.True(t, reload(t, d, "org/app").IsPrivate, "back on auto, the repo decides")

	require.NoError(t, d.SyncAutoPrivate(ctx, p, false))
	assert.False(t, reload(t, d, "org/app").IsPrivate, "auto follows the repo going public")
}

func TestVisibilityFolder_Precedence(t *testing.T) {
	t.Serial()
	ctx := context.Background()
	d := openTestDB(t)
	mustProject(t, d, "org/a", false)
	b := mustProject(t, d, "org/b", false)
	mustProject(t, d, "org/deep/c", true)
	mustProject(t, d, "other/x", false)

	require.NoError(t, d.SetVisibilityFolder(ctx, "org", VisibilityPrivate))
	assert.True(t, reload(t, d, "org/a").IsPrivate)
	assert.True(t, reload(t, d, "org/b").IsPrivate)
	assert.True(t, reload(t, d, "org/deep/c").IsPrivate)
	assert.False(t, reload(t, d, "other/x").IsPrivate, "a folder rule stays inside its folder")

	// The longest folder rule wins.
	require.NoError(t, d.SetVisibilityFolder(ctx, "org/deep", VisibilityPublic))
	assert.False(t, reload(t, d, "org/deep/c").IsPrivate)

	// A project override beats every folder rule.
	require.NoError(t, d.SetProjectVisibilityMode(ctx, b, VisibilityPublic))
	assert.False(t, reload(t, d, "org/b").IsPrivate)

	// A project created under a folder rule gets the rule at birth.
	born := mustProject(t, d, "org/new", false)
	assert.True(t, born.IsPrivate)
	assert.True(t, reload(t, d, "org/new").IsPrivate)

	// Removing the rules restores each project's own auto value.
	require.NoError(t, d.SetVisibilityFolder(ctx, "org", VisibilityAuto))
	require.NoError(t, d.SetVisibilityFolder(ctx, "org/deep", VisibilityAuto))
	assert.False(t, reload(t, d, "org/a").IsPrivate)
	assert.True(t, reload(t, d, "org/deep/c").IsPrivate)
	assert.False(t, reload(t, d, "org/new").IsPrivate)

	folders, err := d.ListVisibilityFolders(ctx)
	require.NoError(t, err)
	assert.Empty(t, folders)

	info, err := d.ListProjectVisibility(ctx)
	require.NoError(t, err)
	assert.Equal(t, VisibilityPublic, info[b.ID].Mode)
}

func TestReleaseAndSiteVisibility(t *testing.T) {
	t.Serial()
	ctx := context.Background()
	d := openTestDB(t)
	p := mustProject(t, d, "app", false)
	rel := &Release{ProjectID: p.ID, Version: "1", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, rel))

	mode, err := d.ReleaseVisibility(ctx, rel.ID)
	require.NoError(t, err)
	assert.Equal(t, VisibilityAuto, mode)

	require.NoError(t, d.SetReleaseVisibility(ctx, rel.ID, VisibilityPrivate))
	mode, err = d.ReleaseVisibility(ctx, rel.ID)
	require.NoError(t, err)
	assert.Equal(t, VisibilityPrivate, mode)
	assert.True(t, mode.Apply(p.IsPrivate))
	all, err := d.ReleaseVisibilities(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, map[int64]Visibility{rel.ID: VisibilityPrivate}, all)

	require.NoError(t, d.SetReleaseVisibility(ctx, rel.ID, VisibilityAuto))
	mode, err = d.ReleaseVisibility(ctx, rel.ID)
	require.NoError(t, err)
	assert.Equal(t, VisibilityAuto, mode)

	require.NoError(t, d.SetSiteVisibility(ctx, p.ID, "feature/x", VisibilityPublic))
	mode, err = d.SiteVisibility(ctx, p.ID, "feature/x")
	require.NoError(t, err)
	assert.Equal(t, VisibilityPublic, mode)
	sites, err := d.SiteVisibilities(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]Visibility{"feature/x": VisibilityPublic}, sites)
	require.NoError(t, d.SetSiteVisibility(ctx, p.ID, "feature/x", VisibilityAuto))
	mode, err = d.SiteVisibility(ctx, p.ID, "feature/x")
	require.NoError(t, err)
	assert.Equal(t, VisibilityAuto, mode)
}
