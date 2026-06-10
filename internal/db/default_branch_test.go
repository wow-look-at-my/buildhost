package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustPublishRelease(t *testing.T, d *DB, projectID int64, version string, versionNum int64, branch string) *Release {
	t.Helper()
	ctx := context.Background()
	r := &Release{ProjectID: projectID, Version: version, VersionNum: versionNum, GitBranch: branch}
	require.NoError(t, d.CreateRelease(ctx, r))
	require.NoError(t, d.PublishRelease(ctx, r.ID))
	return r
}

// The bug: a push to a non-default branch would hijack the apex "latest". With a
// recorded default branch, "latest" must track that branch only.
func TestGetLatestRelease_PrefersDefaultBranch(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	// main ships v1; a feature branch then ships v2 (a higher version_num).
	mustPublishRelease(t, d, p.ID, "1", 1, "main")
	mustPublishRelease(t, d, p.ID, "2", 2, "feature")

	// No default branch recorded yet -> newest across all branches (legacy).
	got, err := d.GetLatestRelease(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "2", got.Version, "no default branch -> global latest")

	// Record main as the default branch: latest now pins to main, not feature.
	require.NoError(t, d.SetProjectDefaultBranch(ctx, p.ID, "main"))
	got, err = d.GetLatestRelease(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "1", got.Version, "default branch pins latest to main")
	assert.Equal(t, "main", got.GitBranch)

	// A newer feature build still must not move latest.
	mustPublishRelease(t, d, p.ID, "3", 3, "feature")
	got, err = d.GetLatestRelease(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "1", got.Version, "feature push cannot hijack latest")

	// A newer main build does move it forward.
	mustPublishRelease(t, d, p.ID, "4", 4, "main")
	got, err = d.GetLatestRelease(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "4", got.Version)
}

// Until the default branch has published anything, fall back to the global
// newest so "latest" is never empty when releases exist.
func TestGetLatestRelease_FallsBackWhenDefaultBranchEmpty(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	require.NoError(t, d.SetProjectDefaultBranch(ctx, p.ID, "main"))
	mustPublishRelease(t, d, p.ID, "1", 1, "feature")

	got, err := d.GetLatestRelease(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "1", got.Version, "no release on default branch yet -> global latest")
}

// Latest considers only published releases, even on the default branch.
func TestGetLatestRelease_IgnoresUnpublishedOnDefaultBranch(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)
	require.NoError(t, d.SetProjectDefaultBranch(ctx, p.ID, "main"))

	mustPublishRelease(t, d, p.ID, "1", 1, "main")
	unpublished := &Release{ProjectID: p.ID, Version: "2", VersionNum: 2, GitBranch: "main"}
	require.NoError(t, d.CreateRelease(ctx, unpublished))

	got, err := d.GetLatestRelease(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "1", got.Version, "unpublished default-branch release is not latest")
}
