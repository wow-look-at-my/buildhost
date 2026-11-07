package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetProjectDepends_StoresSortedAndClears(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	ctx := context.Background()
	p := &Project{Name: "api-cli", Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, p))

	require.NoError(t, d.SetProjectDepends(ctx, p.ID, []string{"zz", "ascii-timeline", "zz"}))
	got, err := d.GetProject(ctx, "api-cli")
	require.NoError(t, err)
	assert.Equal(t, []string{"ascii-timeline", "zz"}, got.DependsOn())

	require.Error(t, d.SetProjectDepends(ctx, p.ID, []string{"a,b"}), "a comma would split one name into two")

	require.NoError(t, d.SetProjectDepends(ctx, p.ID, nil))
	got, err = d.GetProject(ctx, "api-cli")
	require.NoError(t, err)
	assert.Empty(t, got.DependsOn())
}

func TestRenameProject_RewritesDependents(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	ctx := context.Background()
	app := &Project{Name: "api-cli", Versioning: VersioningAuto}
	dep := &Project{Name: "ascii-timeline", Versioning: VersioningAuto}
	other := &Project{Name: "ascii-timeline-extra", Versioning: VersioningAuto}
	for _, p := range []*Project{app, dep, other} {
		require.NoError(t, d.CreateProject(ctx, p))
	}
	require.NoError(t, d.SetProjectDepends(ctx, app.ID, []string{"ascii-timeline", "ascii-timeline-extra"}))

	require.NoError(t, d.RenameProject(ctx, dep.ID, "ascii-timeline", "zz-timeline"))
	got, err := d.GetProject(ctx, "api-cli")
	require.NoError(t, err)
	assert.Equal(t, []string{"ascii-timeline-extra", "zz-timeline"}, got.DependsOn(),
		"only the exact name moves, so a longer name that shares its prefix stays")
}

func TestProjectMerge_RewritesDependents(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	ctx := context.Background()
	src := &Project{Name: "old-dep", Versioning: VersioningAuto, GithubRepoID: "42"}
	dst := &Project{Name: "new-dep", Versioning: VersioningAuto, GithubRepoID: "42"}
	app := &Project{Name: "api-cli", Versioning: VersioningAuto}
	for _, p := range []*Project{src, dst, app} {
		require.NoError(t, d.CreateProject(ctx, p))
	}
	require.NoError(t, d.SetProjectDepends(ctx, app.ID, []string{"old-dep"}))
	require.NoError(t, d.SetProjectDepends(ctx, dst.ID, []string{"old-dep"}))

	plan, err := d.PlanProjectMerge(ctx, "old-dep", "new-dep")
	require.NoError(t, err)
	require.True(t, plan.Applicable(), plan.Conflicts)
	require.NoError(t, d.ApplyProjectMerge(ctx, plan))

	got, err := d.GetProject(ctx, "api-cli")
	require.NoError(t, err)
	assert.Equal(t, []string{"new-dep"}, got.DependsOn())
	got, err = d.GetProject(ctx, "new-dep")
	require.NoError(t, err)
	assert.Empty(t, got.DependsOn(), "the merge target never depends on itself")
}
