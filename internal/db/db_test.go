package db

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
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
	d := openTestDB(t)
	_, err := d.GetProject(context.Background(), "nope")
	assert.True(t, errors.Is(err, ErrNotFound))

}

func TestCreateProjectDuplicateReturnsErrConflict(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	p := &Project{Name: "dup", Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, p))

	p2 := &Project{Name: "dup", Versioning: VersioningAuto}
	err := d.CreateProject(ctx, p2)
	assert.True(t, errors.Is(err, ErrConflict))

}

func TestListProjects(t *testing.T) {
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
	d := openTestDB(t)
	p := createTestProject(t, d)
	_, err := d.GetRelease(context.Background(), p.ID, "9.9.9")
	assert.True(t, errors.Is(err, ErrNotFound))

}

func TestListReleases(t *testing.T) {
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
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	// No releases yet -- should be 1.
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
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	// No published releases yet.
	_, err := d.GetLatestRelease(ctx, p.ID)
	assert.True(t, errors.Is(err, ErrNotFound))

	// Create and publish two releases.
	for i, v := range []string{"1.0.0", "2.0.0"} {
		r := &Release{ProjectID: p.ID, Version: v, VersionNum: int64(i + 1)}
		require.NoError(t, d.CreateRelease(ctx, r))

		require.NoError(t, d.PublishRelease(ctx, r.ID))

	}

	got, err := d.GetLatestRelease(ctx, p.ID)
	require.Nil(t, err)

	assert.Equal(t, "2.0.0", got.Version)

}

func TestGetLatestReleaseByBranch(t *testing.T) {
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

// --- Artifacts ---------------------------------------------------------------

func createTestRelease(t *testing.T, d *DB) (*Project, *Release) {
	t.Helper()
	p := createTestProject(t, d)
	r := &Release{ProjectID: p.ID, Version: "1.0.0", VersionNum: 1}
	require.NoError(t, d.CreateRelease(context.Background(), r))

	return p, r
}

func TestCreateAndGetArtifact(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	a := &Artifact{
		ReleaseID:  r.ID,
		OS:         OSLinux,
		Arch:       ArchAMD64,
		Kind:       KindBinary,
		StorageKey: "deadbeef",
		Size:       1024,
		SHA256:     "aabbccdd",
		Filename:   "mybin",
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	require.NotEqual(t, int64(0), a.ID)

	got, err := d.GetArtifact(ctx, r.ID, string(OSLinux), string(ArchAMD64))
	require.Nil(t, err)

	assert.Equal(t, "deadbeef", got.StorageKey)

	assert.Equal(t, int64(1024), got.Size)

	assert.Equal(t, "mybin", got.Filename)

}

func TestGetArtifactNotFound(t *testing.T) {
	d := openTestDB(t)
	_, r := createTestRelease(t, d)
	_, err := d.GetArtifact(context.Background(), r.ID, "linux", "amd64")
	assert.True(t, errors.Is(err, ErrNotFound))

}

func TestListArtifacts(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	artifacts := []struct {
		os   OS
		arch Arch
	}{
		{OSLinux, ArchAMD64},
		{OSLinux, ArchARM64},
		{OSDarwin, ArchAMD64},
	}
	for _, art := range artifacts {
		a := &Artifact{
			ReleaseID:  r.ID,
			OS:         art.os,
			Arch:       art.arch,
			Kind:       KindBinary,
			StorageKey: "key",
			Size:       100,
			SHA256:     "hash",
		}
		require.NoError(t, d.CreateArtifact(ctx, a))

	}

	list, err := d.ListArtifacts(ctx, r.ID)
	require.Nil(t, err)

	assert.Equal(t, 3, len(list))

}

func TestUpdateArtifactStripped(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	a := &Artifact{
		ReleaseID:  r.ID,
		OS:         OSLinux,
		Arch:       ArchAMD64,
		Kind:       KindBinary,
		StorageKey: "orig-key",
		Size:       2048,
		SHA256:     "origsha",
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	require.NoError(t, d.UpdateArtifactStripped(ctx, a.ID, "strip-key", 1024, "stripsha", "dbg-key", 512))

	got, err := d.GetArtifact(ctx, r.ID, string(OSLinux), string(ArchAMD64))
	require.Nil(t, err)

	assert.Equal(t, "strip-key", got.StrippedStorageKey)

	assert.Equal(t, int64(1024), got.StrippedSize)

	assert.Equal(t, "stripsha", got.StrippedSHA256)

	assert.Equal(t, "dbg-key", got.DebugStorageKey)

	assert.Equal(t, int64(512), got.DebugSize)

}

// --- Packaged Artifacts ------------------------------------------------------

func TestCreateAndGetPackagedArtifact(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	a := &Artifact{
		ReleaseID:  r.ID,
		OS:         OSLinux,
		Arch:       ArchAMD64,
		Kind:       KindBinary,
		StorageKey: "binkey",
		Size:       500,
		SHA256:     "binhash",
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "deb", "debkey", 600, "debhash", "pkg.deb", `{"arch":"amd64"}`))

	key, size, sha, filename, err := d.GetPackagedArtifact(ctx, a.ID, "deb")
	require.Nil(t, err)

	assert.Equal(t, "debkey", key)

	assert.Equal(t, int64(600), size)

	assert.Equal(t, "debhash", sha)

	assert.Equal(t, "pkg.deb", filename)

}

func TestGetPackagedArtifactNotFound(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	a := &Artifact{
		ReleaseID:  r.ID,
		OS:         OSLinux,
		Arch:       ArchAMD64,
		Kind:       KindBinary,
		StorageKey: "k",
		Size:       1,
		SHA256:     "h",
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	_, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "rpm")
	assert.True(t, errors.Is(err, ErrNotFound))

}

func TestCreatePackagedArtifactUpserts(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	a := &Artifact{
		ReleaseID:  r.ID,
		OS:         OSLinux,
		Arch:       ArchAMD64,
		Kind:       KindBinary,
		StorageKey: "k",
		Size:       1,
		SHA256:     "h",
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	// Insert, then replace with different values.
	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "deb", "key1", 100, "sha1", "f1.deb", "{}"))

	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "deb", "key2", 200, "sha2", "f2.deb", "{}"))

	key, size, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "deb")
	require.Nil(t, err)

	assert.Equal(t, "key2", key)

	assert.Equal(t, int64(200), size)

}

// --- Tokens ------------------------------------------------------------------

func TestCreateAndLookupToken(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	plaintext, tok, err := d.CreateToken(ctx, "ci-token", nil, "read,write")
	require.Nil(t, err)

	assert.True(t, strings.HasPrefix(plaintext, "bh_"))

	require.NotEqual(t, int64(0), tok.ID)

	assert.Equal(t, "ci-token", tok.Name)

	assert.Equal(t, "read,write", tok.Scopes)

	looked, err := d.LookupToken(ctx, plaintext)
	require.Nil(t, err)

	assert.Equal(t, tok.ID, looked.ID)

	assert.Equal(t, "ci-token", looked.Name)

}

func TestLookupTokenNotFound(t *testing.T) {
	d := openTestDB(t)
	_, err := d.LookupToken(context.Background(), "bh_bogus_token_value_here")
	assert.True(t, errors.Is(err, ErrNotFound))

}

func TestCreateTokenWithProjectScope(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	p := &Project{Name: "scoped", Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, p))

	pid := p.ID
	_, tok, err := d.CreateToken(ctx, "proj-token", &pid, "read")
	require.Nil(t, err)

	assert.False(t, tok.ProjectID == nil || *tok.ProjectID != p.ID)

}

func TestListTokens(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	for _, name := range []string{"token-a", "token-b", "token-c"} {
		_, _, err := d.CreateToken(ctx, name, nil, "read")
		require.Nil(t, err)

	}

	list, err := d.ListTokens(ctx)
	require.Nil(t, err)

	require.Equal(t, 3, len(list))

}

func TestDeleteToken(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	plaintext, tok, err := d.CreateToken(ctx, "doomed", nil, "read")
	require.Nil(t, err)

	require.NoError(t, d.DeleteToken(ctx, tok.ID))

	_, err = d.LookupToken(ctx, plaintext)
	assert.True(t, errors.Is(err, ErrNotFound))

}

func TestDeleteTokenNotFound(t *testing.T) {
	d := openTestDB(t)
	err := d.DeleteToken(context.Background(), 99999)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestLookupToken_ExpiredTokenReturnsNotFound(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	plaintext, tok, err := d.CreateToken(ctx, "expiring", nil, "read")
	require.NoError(t, err)

	_, err = d.ExecContext(ctx,
		"UPDATE api_tokens SET expires_at = datetime('now', '-1 hour') WHERE id = ?", tok.ID)
	require.NoError(t, err)

	_, err = d.LookupToken(ctx, plaintext)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestLookupToken_FutureExpirySucceeds(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	plaintext, tok, err := d.CreateToken(ctx, "valid-future", nil, "read")
	require.NoError(t, err)

	_, err = d.ExecContext(ctx,
		"UPDATE api_tokens SET expires_at = datetime('now', '+1 hour') WHERE id = ?", tok.ID)
	require.NoError(t, err)

	got, err := d.LookupToken(ctx, plaintext)
	require.NoError(t, err)
	assert.Equal(t, tok.ID, got.ID)
}

// --- Download Counts ---------------------------------------------------------

func TestIncrementDownloadCount(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	a := &Artifact{
		ReleaseID:  r.ID,
		OS:         OSLinux,
		Arch:       ArchAMD64,
		Kind:       KindBinary,
		StorageKey: "key1",
		Size:       100,
		SHA256:     "hash1",
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	require.NoError(t, d.IncrementDownloadCount(ctx, a.ID))
	require.NoError(t, d.IncrementDownloadCount(ctx, a.ID))
	require.NoError(t, d.IncrementDownloadCount(ctx, a.ID))

	total, err := d.GetTotalDownloads(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
}

func TestGetTotalDownloadsNoDownloads(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	total, err := d.GetTotalDownloads(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
}

func TestListArtifactDetails(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	a := &Artifact{
		ReleaseID:  r.ID,
		OS:         OSLinux,
		Arch:       ArchAMD64,
		Kind:       KindBinary,
		StorageKey: "binkey",
		Size:       500,
		SHA256:     "binhash",
		Filename:   "mybin",
	}
	require.NoError(t, d.CreateArtifact(ctx, a))
	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "deb", "debkey", 600, "debhash", "pkg.deb", "{}"))
	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "npm", "npmkey", 550, "npmhash", "pkg.tgz", "{}"))
	require.NoError(t, d.IncrementDownloadCount(ctx, a.ID))
	require.NoError(t, d.IncrementDownloadCount(ctx, a.ID))

	details, pkgs, err := d.ListArtifactDetails(ctx, r.ID)
	require.NoError(t, err)
	require.Equal(t, 1, len(details))

	detail := details[0]
	assert.Equal(t, "mybin", detail.Filename)
	assert.Equal(t, int64(500), detail.Size)
	assert.Equal(t, int64(2), detail.DownloadCount)
	require.Equal(t, 2, len(pkgs[0]))
	assert.Equal(t, "deb", pkgs[0][0].Format)
	assert.Equal(t, "npm", pkgs[0][1].Format)
}

func TestListArtifactDetailsEmpty(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	details, _, err := d.ListArtifactDetails(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, len(details))
}
