package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePlatformList(t *testing.T) {
	got, err := ParsePlatformList("linux/amd64, macOS/aarch64 ,Windows/X64")
	require.NoError(t, err)
	assert.Equal(t, []Platform{
		{OS: OSLinux, Arch: ArchAMD64},
		{OS: OSDarwin, Arch: ArchARM64},
		{OS: OSWindows, Arch: ArchAMD64},
	}, got)
	assert.Equal(t, "linux/amd64, darwin/arm64, windows/amd64", FormatPlatforms(got))
}

func TestParsePlatformList_Rejects(t *testing.T) {
	for name, spec := range map[string]string{
		"empty":         "",
		"no slash":      "linux",
		"unknown os":    "plan9/amd64",
		"unknown arch":  "linux/sparc",
		"empty element": "linux/amd64,,darwin/arm64",
		"duplicate":     "linux/amd64,linux/amd64",
		"alias dup":     "darwin/arm64,macos/aarch64",
		"wasm mismatch": "linux/wasip1",
		"os only":       "linux/",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParsePlatformList(spec)
			assert.Error(t, err)
		})
	}
}

// seedPlatformRelease creates a project plus an unpublished release to hang
// artifacts off.
func seedPlatformRelease(t *testing.T, d *DB, name string) *Release {
	t.Helper()
	ctx := context.Background()
	p := &Project{Name: name, Versioning: VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, p))
	r := &Release{ProjectID: p.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, r))
	return r
}

func TestCreateMultiPlatformArtifact_OneRowManySlots(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	rel := seedPlatformRelease(t, d, "ape-one-row")

	platforms := []Platform{
		{OS: OSLinux, Arch: ArchAMD64},
		{OS: OSDarwin, Arch: ArchARM64},
		{OS: OSWindows, Arch: ArchAMD64},
	}
	a := &Artifact{ReleaseID: rel.ID, Kind: KindBinary, StorageKey: "key", Size: 10, SHA256: "key", ExeFormat: "ape"}
	require.NoError(t, d.CreateMultiPlatformArtifact(ctx, a, platforms))

	rows, err := d.ListArtifacts(ctx, rel.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, OSLinux, rows[0].OS)
	assert.Equal(t, ArchAMD64, rows[0].Arch)

	got, err := d.ArtifactPlatforms(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, platforms, got)

	for _, p := range platforms {
		resolved, err := d.GetArtifact(ctx, rel.ID, string(p.OS), string(p.Arch))
		require.NoError(t, err, "%s", p)
		assert.Equal(t, a.ID, resolved.ID, "%s", p)
	}

	// Every covered platform folds to the canonical slot.
	for _, p := range platforms {
		os, arch, err := d.CanonicalPlatform(ctx, rel.ID, string(p.OS), string(p.Arch))
		require.NoError(t, err)
		assert.Equal(t, "linux", os)
		assert.Equal(t, "amd64", arch)
	}
}

func TestCreateMultiPlatformArtifact_ConflictLeavesNothing(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	rel := seedPlatformRelease(t, d, "ape-conflict")

	existing := &Artifact{ReleaseID: rel.ID, OS: OSWindows, Arch: ArchAMD64, Kind: KindBinary, StorageKey: "w", Size: 1, SHA256: "w"}
	require.NoError(t, d.CreateArtifact(ctx, existing))

	a := &Artifact{ReleaseID: rel.ID, Kind: KindBinary, StorageKey: "key", Size: 10, SHA256: "key"}
	err := d.CreateMultiPlatformArtifact(ctx, a, []Platform{
		{OS: OSLinux, Arch: ArchAMD64},
		{OS: OSWindows, Arch: ArchAMD64},
	})
	require.ErrorIs(t, err, ErrConflict)
	assert.Contains(t, err.Error(), "windows/amd64")

	rows, err := d.ListArtifacts(ctx, rel.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	_, err = d.GetArtifact(ctx, rel.ID, "linux", "amd64")
	assert.ErrorIs(t, err, ErrNotFound)
}

// A different kind may occupy the same platform, exactly as before: the slot
// index is per (release, kind, os, arch).
func TestCreateMultiPlatformArtifact_KindsShareAPlatform(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	rel := seedPlatformRelease(t, d, "ape-kinds")

	bin := &Artifact{ReleaseID: rel.ID, Kind: KindBinary, StorageKey: "b", Size: 1, SHA256: "b"}
	require.NoError(t, d.CreateMultiPlatformArtifact(ctx, bin, []Platform{{OS: OSLinux, Arch: ArchAMD64}}))
	lib := &Artifact{ReleaseID: rel.ID, Kind: KindLibrary, StorageKey: "l", Size: 1, SHA256: "l"}
	require.NoError(t, d.CreateMultiPlatformArtifact(ctx, lib, []Platform{{OS: OSLinux, Arch: ArchAMD64}}))
}

// ListArtifactsByPlatform is what apt/brew/npm/oci consume: a multi-platform
// artifact reaches every platform it covers, each with its own cache key.
func TestListArtifactsByPlatform(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	rel := seedPlatformRelease(t, d, "ape-flatten")

	a := &Artifact{ReleaseID: rel.ID, Kind: KindBinary, StorageKey: "key", Size: 10, SHA256: "key", ExeFormat: "ape"}
	require.NoError(t, d.CreateMultiPlatformArtifact(ctx, a, []Platform{
		{OS: OSLinux, Arch: ArchAMD64},
		{OS: OSLinux, Arch: ArchARM64},
	}))

	got, err := d.ListArtifactsByPlatform(ctx, rel.ID)
	require.NoError(t, err)
	require.Len(t, got, 2)

	assert.Equal(t, ArchAMD64, got[0].Arch)
	assert.Empty(t, got[0].CacheSuffix, "the canonical slot keeps the pre-existing cache key")
	assert.Equal(t, "deb", got[0].CacheFormat("deb"))

	assert.Equal(t, ArchARM64, got[1].Arch)
	assert.Equal(t, "deb@linux/arm64", got[1].CacheFormat("deb"),
		"a second platform of one file must not overwrite the first's cached package")
	assert.Equal(t, got[0].ID, got[1].ID)
}

// ListArtifactsWithPlatforms is the one-row-per-file view the UI and REST API
// render; every artifact carries a non-empty set.
func TestListArtifactsWithPlatforms_SinglePlatformStillHasASet(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	rel := seedPlatformRelease(t, d, "ape-uniform")

	require.NoError(t, d.CreateArtifact(ctx, &Artifact{
		ReleaseID: rel.ID, OS: OSDarwin, Arch: ArchARM64, Kind: KindBinary, StorageKey: "k", Size: 1, SHA256: "k",
	}))

	got, err := d.ListArtifactsWithPlatforms(ctx, rel.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, []Platform{{OS: OSDarwin, Arch: ArchARM64}}, got[0].Platforms)
	assert.False(t, got[0].MultiPlatform())
}
