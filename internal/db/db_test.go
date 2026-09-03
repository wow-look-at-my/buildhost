package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	require.Nil(t, err)

	t.Cleanup(func() { d.Close() })
	return d
}

// --- Projects ----------------------------------------------------------------

func TestCreateAndGetProject(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	p := &Project{
		Name:        "myproject",
		Description: "A test project",
		Homepage:    "https://example.com",
		License:     "MIT",
		IsPrivate:   false,
		Versioning:  VersioningAuto,
	}
	require.NoError(t, d.CreateProject(ctx, p))

	require.NotEqual(t, int64(0), p.ID)

	got, err := d.GetProject(ctx, "myproject")
	require.Nil(t, err)

	assert.Equal(t, "myproject", got.Name)

	assert.Equal(t, "A test project", got.Description)

	assert.Equal(t, "MIT", got.License)

	assert.Equal(t, VersioningAuto, got.Versioning)

}

func TestGetProjectNotFound(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	_, err := d.GetProject(context.Background(), "nope")
	assert.True(t, errors.Is(err, ErrNotFound))

}

func TestCreateProjectDuplicateReturnsErrConflict(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	p := &Project{Name: "dup", Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, p))

	p2 := &Project{Name: "dup", Versioning: VersioningAuto}
	err := d.CreateProject(ctx, p2)
	assert.True(t, errors.Is(err, ErrConflict))

}

func TestListProjects(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	for _, name := range []string{"bravo", "alpha", "charlie"} {
		p := &Project{Name: name, Versioning: VersioningAuto}
		require.NoError(t, d.CreateProject(ctx, p))

	}

	list, err := d.ListProjects(ctx)
	require.Nil(t, err)

	require.Equal(t, 3, len(list))

	// Projects are ordered by name.
	assert.False(t, list[0].Name != "alpha" || list[1].Name != "bravo" || list[2].Name != "charlie")

}

// --- Releases ----------------------------------------------------------------

func createTestProject(t *testing.T, d *DB) *Project {
	t.Helper()
	p := &Project{Name: "relpkg", Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(context.Background(), p))

	return p
}

func TestCreateAndGetRelease(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	r := &Release{
		ProjectID:  p.ID,
		Version:    "1.0.0",
		VersionNum: 1,
		GitBranch:  "main",
		GitCommit:  "abc123",
		Notes:      "first release",
	}
	require.NoError(t, d.CreateRelease(ctx, r))

	require.NotEqual(t, int64(0), r.ID)

	got, err := d.GetRelease(ctx, p.ID, "1.0.0")
	require.Nil(t, err)

	assert.Equal(t, "1.0.0", got.Version)

	assert.Equal(t, "main", got.GitBranch)

	assert.False(t, got.Published)

}

func TestGetReleaseNotFound(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	p := createTestProject(t, d)
	_, err := d.GetRelease(context.Background(), p.ID, "9.9.9")
	assert.True(t, errors.Is(err, ErrNotFound))

}

func TestListReleases(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	for i, v := range []string{"1.0.0", "2.0.0", "3.0.0"} {
		r := &Release{
			ProjectID:  p.ID,
			Version:    v,
			VersionNum: int64(i + 1),
		}
		require.NoError(t, d.CreateRelease(ctx, r))

	}

	list, err := d.ListReleases(ctx, p.ID)
	require.Nil(t, err)

	require.Equal(t, 3, len(list))

	// Ordered by version_num DESC.
	assert.Equal(t, "3.0.0", list[0].Version)

}

func TestNextVersionNum(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	num, err := d.NextVersionNum(ctx, p.ID)
	require.Nil(t, err)

	assert.Equal(t, int64(1), num)

	r := &Release{ProjectID: p.ID, Version: "1.0.0", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, r))

	num, err = d.NextVersionNum(ctx, p.ID)
	require.Nil(t, err)

	assert.Equal(t, int64(2), num)

}

func TestPublishRelease(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	r := &Release{ProjectID: p.ID, Version: "1.0.0", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, r))

	require.NoError(t, d.PublishRelease(ctx, r.ID))

	got, err := d.GetRelease(ctx, p.ID, "1.0.0")
	require.Nil(t, err)

	assert.True(t, got.Published)

	assert.NotNil(t, got.PublishedAt)

}

func TestGetLatestRelease(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	// No published releases yet.
	_, err := d.GetLatestRelease(ctx, p.ID)
	assert.True(t, errors.Is(err, ErrNotFound))

	releases := []struct {
		version string
		num     int64
		branch  string
	}{
		{"1.0.0", 1, "master"},
		{"2.0.0", 2, "feature-x"},
	}
	for _, rl := range releases {
		r := &Release{
			ProjectID:  p.ID,
			Version:    rl.version,
			VersionNum: rl.num,
			GitBranch:  rl.branch,
		}
		require.NoError(t, d.CreateRelease(ctx, r))

		require.NoError(t, d.PublishRelease(ctx, r.ID))

	}

	got, err := d.GetLatestRelease(ctx, p.ID)
	require.Nil(t, err)

	assert.Equal(t, "1.0.0", got.Version)

}

func TestGetLatestReleaseByBranch(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	releases := []struct {
		version string
		num     int64
		branch  string
	}{
		{"1.0.0", 1, "main"},
		{"2.0.0", 2, "main"},
		{"3.0.0-rc1", 3, "develop"},
	}
	for _, rl := range releases {
		r := &Release{
			ProjectID:  p.ID,
			Version:    rl.version,
			VersionNum: rl.num,
			GitBranch:  rl.branch,
		}
		require.NoError(t, d.CreateRelease(ctx, r))

		require.NoError(t, d.PublishRelease(ctx, r.ID))

	}

	got, err := d.GetLatestReleaseByBranch(ctx, p.ID, "main")
	require.Nil(t, err)

	assert.Equal(t, "2.0.0", got.Version)

	got, err = d.GetLatestReleaseByBranch(ctx, p.ID, "develop")
	require.Nil(t, err)

	assert.Equal(t, "3.0.0-rc1", got.Version)

	_, err = d.GetLatestReleaseByBranch(ctx, p.ID, "nonexistent")
	assert.True(t, errors.Is(err, ErrNotFound))

}
