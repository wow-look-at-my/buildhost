package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Download Events ---------------------------------------------------------

func TestRecordAndListDownloadEvents(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p, r := createTestRelease(t, d)

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

	// Anonymous public pull, then an authenticated token pull.
	require.NoError(t, d.RecordDownloadEvent(ctx, a.ID, "raw", "203.0.113.7", "curl/8.5.0", ""))
	require.NoError(t, d.RecordDownloadEvent(ctx, a.ID, "tar.gz", "198.51.100.4", "Homebrew/4.2", "token:ci"))

	byProject, err := d.ListDownloadEventsByProject(ctx, p.ID, 10)
	require.NoError(t, err)
	require.Len(t, byProject, 2)

	first := byProject[0]
	assert.Equal(t, "tar.gz", first.Fmt)
	assert.Equal(t, "198.51.100.4", first.ClientIp)
	assert.Equal(t, "Homebrew/4.2", first.UserAgent)
	assert.Equal(t, "token:ci", first.Principal)
	assert.Equal(t, OSLinux, first.OS)
	assert.Equal(t, ArchAMD64, first.Arch)
	assert.Equal(t, r.Version, first.Version)

	assert.Equal(t, "raw", byProject[1].Fmt)
	assert.Empty(t, byProject[1].Principal, "anonymous download has no principal")

	byRelease, err := d.ListDownloadEventsByRelease(ctx, r.ID, 10)
	require.NoError(t, err)
	require.Len(t, byRelease, 2)
	assert.Equal(t, "tar.gz", byRelease[0].Fmt)
	assert.Equal(t, "203.0.113.7", byRelease[1].ClientIp)

	// limit is honored.
	limited, err := d.ListDownloadEventsByRelease(ctx, r.ID, 1)
	require.NoError(t, err)
	assert.Len(t, limited, 1)
}

func TestListDownloadEventsEmpty(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	p, r := createTestRelease(t, d)

	byProject, err := d.ListDownloadEventsByProject(ctx, p.ID, 10)
	require.NoError(t, err)
	assert.Empty(t, byProject)

	byRelease, err := d.ListDownloadEventsByRelease(ctx, r.ID, 10)
	require.NoError(t, err)
	assert.Empty(t, byRelease)
}
