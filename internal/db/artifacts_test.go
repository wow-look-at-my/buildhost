package db

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	key, size, sha, filename, metadata, err := d.GetPackagedArtifact(ctx, a.ID, "deb")
	require.Nil(t, err)

	assert.Equal(t, "debkey", key)

	assert.Equal(t, int64(600), size)

	assert.Equal(t, "debhash", sha)

	assert.Equal(t, "pkg.deb", filename)

	assert.Equal(t, `{"arch":"amd64"}`, metadata)

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

	_, _, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "rpm")
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

	key, size, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "deb")
	require.Nil(t, err)

	assert.Equal(t, "key2", key)

	assert.Equal(t, int64(200), size)

}
