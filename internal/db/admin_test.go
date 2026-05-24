package db

import (
	"context"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestGetDashboardStats_Empty(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	stats, err := d.GetDashboardStats(ctx)
	require.NoError(t, err)

	assert.Equal(t, int64(0), stats.ProjectCount)
	assert.Equal(t, int64(0), stats.ReleaseCount)
	assert.Equal(t, int64(0), stats.ArtifactCount)
	assert.Equal(t, int64(0), stats.TotalStorageBytes)
	assert.Equal(t, int64(0), stats.TokenCount)
	assert.Equal(t, int64(0), stats.OidcPolicyCount)
	assert.Equal(t, int64(0), stats.LogicalBytes)
	assert.Equal(t, int64(0), stats.PhysicalBytes)
}

func TestGetDashboardStats_WithData(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	p := &Project{Name: "statsproject", Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, p))

	r := &Release{ProjectID: p.ID, Version: "1.0.0", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, r))

	a := &Artifact{
		ReleaseID:  r.ID,
		OS:         OSLinux,
		Arch:       ArchAMD64,
		Kind:       KindBinary,
		StorageKey: "abc",
		Size:       4096,
		SHA256:     "deadbeef",
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	_, _, err := d.CreateToken(ctx, "tok", nil, "read")
	require.NoError(t, err)

	require.NoError(t, d.CreateOIDCPolicy(ctx, &OIDCPolicy{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "repo:org/repo:*",
		Scopes:         "read",
	}))

	stats, err := d.GetDashboardStats(ctx)
	require.NoError(t, err)

	assert.Equal(t, int64(1), stats.ProjectCount)
	assert.Equal(t, int64(1), stats.ReleaseCount)
	assert.Equal(t, int64(1), stats.ArtifactCount)
	assert.Equal(t, int64(4096), stats.TotalStorageBytes)
	assert.Equal(t, int64(1), stats.TokenCount)
	assert.Equal(t, int64(1), stats.OidcPolicyCount)
	assert.Equal(t, int64(4096), stats.LogicalBytes)
	assert.Equal(t, int64(4096), stats.PhysicalBytes)
}

func TestGetDashboardStats_DedupRatio(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	p := &Project{Name: "dedupproject", Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, p))

	r := &Release{ProjectID: p.ID, Version: "1.0.0", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, r))

	// Two artifacts sharing the same storage_key (deduplication).
	a1 := &Artifact{
		ReleaseID: r.ID, OS: OSLinux, Arch: ArchAMD64,
		Kind: KindBinary, StorageKey: "aaa", Size: 1000, SHA256: "aaa",
	}
	require.NoError(t, d.CreateArtifact(ctx, a1))

	a2 := &Artifact{
		ReleaseID: r.ID, OS: OSDarwin, Arch: ArchAMD64,
		Kind: KindBinary, StorageKey: "aaa", Size: 1000, SHA256: "aaa",
	}
	require.NoError(t, d.CreateArtifact(ctx, a2))

	// Add stripped/debug to a1 with unique keys.
	require.NoError(t, d.UpdateArtifactStripped(ctx, a1.ID, "bbb", 800, "bbb", "ccc", 200))

	// Add a packaged artifact reusing a1's stripped key.
	require.NoError(t, d.CreatePackagedArtifact(ctx, a1.ID, "deb", "bbb", 800, "bbb", "pkg.deb", "{}"))

	stats, err := d.GetDashboardStats(ctx)
	require.NoError(t, err)

	// Logical: a1.size(1000) + a2.size(1000) + a1.stripped(800) + a1.debug(200) + pkg(800) = 3800
	assert.Equal(t, int64(3800), stats.LogicalBytes)
	// Physical: unique keys: "aaa"(1000) + "bbb"(800) + "ccc"(200) = 2000
	assert.Equal(t, int64(2000), stats.PhysicalBytes)
}

func TestListRecentReleases(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	p := &Project{Name: "recentproj", Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, p))

	for i, v := range []string{"1.0.0", "2.0.0", "3.0.0"} {
		r := &Release{ProjectID: p.ID, Version: v, VersionNum: int64(i + 1)}
		require.NoError(t, d.CreateRelease(ctx, r))
	}

	recent, err := d.ListRecentReleases(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, 2, len(recent))
	assert.Equal(t, "3.0.0", recent[0].Version)
	assert.Equal(t, "recentproj", recent[0].ProjectName)
}

func TestListRecentReleases_Empty(t *testing.T) {
	d := openTestDB(t)
	recent, err := d.ListRecentReleases(context.Background(), 10)
	require.NoError(t, err)
	assert.Equal(t, 0, len(recent))
}

func TestListProjectSummaries(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	p := &Project{Name: "alpha", Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, p))

	r := &Release{ProjectID: p.ID, Version: "1.0.0", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, r))

	a := &Artifact{
		ReleaseID: r.ID, OS: OSLinux, Arch: ArchAMD64,
		Kind: KindBinary, StorageKey: "k", Size: 100, SHA256: "h",
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	p2 := &Project{Name: "beta", Versioning: VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, p2))

	summaries, err := d.ListProjectSummaries(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, len(summaries))

	assert.Equal(t, "alpha", summaries[0].Name)
	assert.Equal(t, int64(1), summaries[0].ReleaseCount)
	assert.Equal(t, int64(1), summaries[0].ArtifactCount)

	assert.Equal(t, "beta", summaries[1].Name)
	assert.Equal(t, int64(0), summaries[1].ReleaseCount)
	assert.Equal(t, int64(0), summaries[1].ArtifactCount)
}

func TestListReleaseSummaries(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	p := &Project{Name: "relsum", Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, p))

	r := &Release{ProjectID: p.ID, Version: "1.0.0", VersionNum: 1, GitBranch: "main"}
	require.NoError(t, d.CreateRelease(ctx, r))

	a := &Artifact{
		ReleaseID: r.ID, OS: OSLinux, Arch: ArchAMD64,
		Kind: KindBinary, StorageKey: "k", Size: 50, SHA256: "h",
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	summaries, err := d.ListReleaseSummaries(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, 1, len(summaries))
	assert.Equal(t, "1.0.0", summaries[0].Version)
	assert.Equal(t, int64(1), summaries[0].ArtifactCount)
}

func TestListTokenDetails(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	p := &Project{Name: "tokproj", Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, p))

	_, _, err := d.CreateToken(ctx, "global-tok", nil, "read,write")
	require.NoError(t, err)

	pid := p.ID
	_, _, err = d.CreateToken(ctx, "proj-tok", &pid, "read")
	require.NoError(t, err)

	tokens, err := d.ListTokenDetails(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, len(tokens))

	found := false
	for _, tok := range tokens {
		if tok.Name == "proj-tok" {
			assert.Equal(t, "tokproj", tok.ProjectName)
			found = true
		}
		if tok.Name == "global-tok" {
			assert.Equal(t, "", tok.ProjectName)
		}
	}
	assert.True(t, found)
}

func TestListOIDCPolicyDetails(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	p := &Project{Name: "oidcproj", Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, p))

	pid := p.ID
	require.NoError(t, d.CreateOIDCPolicy(ctx, &OIDCPolicy{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "repo:org/repo:*",
		ProjectID:      &pid,
		Scopes:         "read,write",
	}))

	require.NoError(t, d.CreateOIDCPolicy(ctx, &OIDCPolicy{
		Issuer:         "https://accounts.google.com",
		SubjectPattern: "*",
		Scopes:         "read",
	}))

	policies, err := d.ListOIDCPolicyDetails(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, len(policies))

	found := false
	for _, pol := range policies {
		if pol.Issuer == "https://token.actions.githubusercontent.com" {
			assert.Equal(t, "oidcproj", pol.ProjectName)
			found = true
		}
		if pol.Issuer == "https://accounts.google.com" {
			assert.Equal(t, "", pol.ProjectName)
		}
	}
	assert.True(t, found)
}
