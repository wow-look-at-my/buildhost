package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Download Counts ---------------------------------------------------------

func TestIncrementDownloadCount(t *testing.T) {
	t.Serial()
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
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	total, err := d.GetTotalDownloads(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
}

func TestListArtifactDetails(t *testing.T) {
	t.Serial()
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
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	details, _, err := d.ListArtifactDetails(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, len(details))
}
