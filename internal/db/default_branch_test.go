package db

import (
	"context"
	"errors"
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

// The bug: a push to a non-default branch hijacked the apex "latest". master is
// the default value of a project's default branch, so a project that never set
// one tracks master only.
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

// Until master has published anything, "latest" is empty even if other
// branches have published releases.
func TestGetLatestRelease_NoLatestWhenNoMaster(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	mustPublishRelease(t, d, p.ID, "1", 1, "feature")

	_, err := d.GetLatestRelease(ctx, p.ID)
	assert.True(t, errors.Is(err, ErrNotFound), "feature-only releases must not become latest")
}

// A project whose default branch is not master (e.g. go-toolchain releases off
// "v1") resolves "latest" against that branch -- the regression that left
// go-toolchain with no build tagged "latest".
func TestGetLatestRelease_PerProjectDefaultBranch(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)
	require.NoError(t, d.SetProjectDefaultBranch(ctx, p.ID, "v1"))

	// Releases land on v1 (the default) and on a feature branch with a higher num.
	mustPublishRelease(t, d, p.ID, "1", 1, "v1")
	mustPublishRelease(t, d, p.ID, "2", 2, "feature")

	got, err := d.GetLatestRelease(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "1", got.Version, "latest must track the project's default branch (v1)")
	assert.Equal(t, "v1", got.GitBranch)

	// A newer feature build must not move latest; a newer v1 build does.
	mustPublishRelease(t, d, p.ID, "3", 3, "feature")
	mustPublishRelease(t, d, p.ID, "4", 4, "v1")
	got, err = d.GetLatestRelease(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "4", got.Version)
}

// Once a project's default branch is not master, master loses its special
// status: a master-only release is no longer the apex "latest".
func TestGetLatestRelease_NonMasterDefaultIgnoresMaster(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)
	require.NoError(t, d.SetProjectDefaultBranch(ctx, p.ID, "v1"))

	mustPublishRelease(t, d, p.ID, "1", 1, "master")

	_, err := d.GetLatestRelease(ctx, p.ID)
	assert.True(t, errors.Is(err, ErrNotFound), "master is not latest when the default branch is v1")
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
