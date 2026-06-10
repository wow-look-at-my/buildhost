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

// The bug: a push to a non-default branch hijacked the apex "latest". buildhost
// assumes master is the default branch, so "latest" must track master only.
func TestGetLatestRelease_PrefersMaster(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	// master ships v1; a feature branch then ships v2 (a higher version_num).
	mustPublishRelease(t, d, p.ID, "1", 1, "master")
	mustPublishRelease(t, d, p.ID, "2", 2, "feature")

	got, err := d.GetLatestRelease(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "1", got.Version, "feature push must not hijack latest")
	assert.Equal(t, "master", got.GitBranch)

	// A newer feature build still must not move latest.
	mustPublishRelease(t, d, p.ID, "3", 3, "feature")
	got, err = d.GetLatestRelease(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "1", got.Version)

	// A newer master build does move it forward.
	mustPublishRelease(t, d, p.ID, "4", 4, "master")
	got, err = d.GetLatestRelease(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "4", got.Version)
}

// Until master has published anything, fall back to the global newest so
// "latest" is never empty when releases exist.
func TestGetLatestRelease_FallsBackWhenNoMaster(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	mustPublishRelease(t, d, p.ID, "1", 1, "feature")

	got, err := d.GetLatestRelease(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "1", got.Version, "no master release yet -> global latest")
}

// Latest considers only published releases, even on master.
func TestGetLatestRelease_IgnoresUnpublishedMaster(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	mustPublishRelease(t, d, p.ID, "1", 1, "master")
	unpublished := &Release{ProjectID: p.ID, Version: "2", VersionNum: 2, GitBranch: "master"}
	require.NoError(t, d.CreateRelease(ctx, unpublished))

	got, err := d.GetLatestRelease(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "1", got.Version, "unpublished master release is not latest")
}
