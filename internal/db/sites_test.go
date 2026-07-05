package db

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createSiteTestProject(t *testing.T, d *DB) *Project {
	t.Helper()
	p := &Project{Name: "siteproj", Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(context.Background(), p))
	return p
}

func TestUpsertSite_CreateNew(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createSiteTestProject(t, d)

	s := &Site{
		ProjectID:  p.ID,
		Branch:     "main",
		StorageKey: "abc123",
		Size:       1024,
		SHA256:     "deadbeef",
		FileCount:  5,
		GitCommit:  "aaa111",
	}
	oldKey, err := d.UpsertSite(ctx, s)
	require.NoError(t, err)
	assert.Equal(t, "", oldKey)
	assert.NotEqual(t, int64(0), s.ID)
}

func TestUpsertSite_ReplaceExisting(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createSiteTestProject(t, d)

	s1 := &Site{
		ProjectID:  p.ID,
		Branch:     "main",
		StorageKey: "key1",
		Size:       100,
		SHA256:     "sha1",
		FileCount:  3,
	}
	_, err := d.UpsertSite(ctx, s1)
	require.NoError(t, err)

	s2 := &Site{
		ProjectID:  p.ID,
		Branch:     "main",
		StorageKey: "key2",
		Size:       200,
		SHA256:     "sha2",
		FileCount:  7,
	}
	oldKey, err := d.UpsertSite(ctx, s2)
	require.NoError(t, err)
	assert.Equal(t, "key1", oldKey)

	got, err := d.GetSite(ctx, p.ID, "main")
	require.NoError(t, err)
	assert.Equal(t, "key2", got.StorageKey)
	assert.Equal(t, int64(200), got.Size)
	assert.Equal(t, int64(7), got.FileCount)
}

func TestGetSite_NotFound(t *testing.T) {
	d := openTestDB(t)
	_, err := d.GetSite(context.Background(), 999, "nope")
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestListSites_Empty(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createSiteTestProject(t, d)

	sites, err := d.ListSites(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, len(sites))
}

func TestListSites_MultipleBranches(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createSiteTestProject(t, d)

	for _, branch := range []string{"main", "dev", "feature"} {
		s := &Site{
			ProjectID:  p.ID,
			Branch:     branch,
			StorageKey: "key-" + branch,
			Size:       100,
			SHA256:     "sha-" + branch,
			FileCount:  1,
		}
		_, err := d.UpsertSite(ctx, s)
		require.NoError(t, err)
	}

	sites, err := d.ListSites(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, len(sites))
}

func TestDeleteSite(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createSiteTestProject(t, d)

	s := &Site{
		ProjectID:  p.ID,
		Branch:     "main",
		StorageKey: "delkey",
		Size:       100,
		SHA256:     "sha",
		FileCount:  1,
	}
	_, err := d.UpsertSite(ctx, s)
	require.NoError(t, err)

	key, err := d.DeleteSite(ctx, p.ID, "main")
	require.NoError(t, err)
	assert.Equal(t, "delkey", key)

	_, err = d.GetSite(ctx, p.ID, "main")
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestDeleteSite_NotFound(t *testing.T) {
	d := openTestDB(t)
	_, err := d.DeleteSite(context.Background(), 999, "nope")
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestDeleteSitesByRepositoryBranch(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	for _, name := range []string{"my_repo", "my_repo/cli", "myXrepo"} {
		p := &Project{Name: name, Versioning: VersioningAuto}
		require.NoError(t, d.CreateProject(ctx, p))
		_, err := d.UpsertSite(ctx, &Site{
			ProjectID:  p.ID,
			Branch:     "feature",
			StorageKey: "key-" + name,
			Size:       100,
			SHA256:     "sha-" + name,
			FileCount:  1,
		})
		require.NoError(t, err)
	}

	root, err := d.GetProject(ctx, "my_repo")
	require.NoError(t, err)
	_, err = d.UpsertSite(ctx, &Site{
		ProjectID:  root.ID,
		Branch:     "main",
		StorageKey: "key-main",
		Size:       100,
		SHA256:     "sha-main",
		FileCount:  1,
	})
	require.NoError(t, err)

	deleted, err := d.DeleteSitesByRepositoryBranch(ctx, "my_repo", "feature")
	require.NoError(t, err)
	require.Len(t, deleted, 2)

	_, err = d.GetSite(ctx, root.ID, "feature")
	assert.True(t, errors.Is(err, ErrNotFound))
	_, err = d.GetSite(ctx, root.ID, "main")
	assert.NoError(t, err)

	other, err := d.GetProject(ctx, "myXrepo")
	require.NoError(t, err)
	_, err = d.GetSite(ctx, other.ID, "feature")
	assert.NoError(t, err)
}
