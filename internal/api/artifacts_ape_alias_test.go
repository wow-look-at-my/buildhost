package api

// Tests for publishing ONE Actually Portable Executable as ONE artifact: the
// {os}/{arch} alias spellings no longer fan an APE out into a row per platform,
// and a declared platform is checked against the bytes that would serve it.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// apeWithPESections builds a minimal APE body whose PE header declares nsect
// sections, so a test can choose between the real header and the do-nothing
// stub. 0x80 is where a gosmopolitan APE puts its PE header.
func apeWithPESections(t *testing.T, nsect uint16) string {
	t.Helper()
	const peOff = 0x80
	b := make([]byte, peOff+8)
	copy(b, "MZqFpD")
	binary.LittleEndian.PutUint32(b[0x3c:], peOff)
	binary.LittleEndian.PutUint16(b[peOff+6:], nsect)
	return string(b)
}

// TestUploadArtifact_CosmoAliasAPEIsOneRow is the directive the alias predates:
// one file that runs on N platforms is ONE artifact with N slots, not N rows
// with N download links. The alias spelling stays; what it produces changed.
func TestUploadArtifact_CosmoAliasAPEIsOneRow(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "cosmoapeproj")
	counting := &countingStore{Storage: h.Store}
	h.Store = counting

	rec := doUpload(t, h, proj, "cosmo", "amd64", "", apeWithPESections(t, 3))
	require.Equal(t, http.StatusCreated, rec.Code)

	var got db.ArtifactWithPlatforms
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, []string{"linux/amd64", "darwin/amd64", "windows/amd64"}, platformStrings(got.Platforms))
	assert.Equal(t, "ape", got.ExeFormat)

	assert.Equal(t, 1, counting.puts, "the blob is stored once")
	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1, "one APE must be one artifact row, not one per platform")
}

// TestUploadArtifact_NonAPEAliasStillFansOut pins the other half: when the
// combinations really are separate builds that happen to share bytes, a row
// each is still the right answer.
func TestUploadArtifact_NonAPEAliasStillFansOut(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "nonapealiasproj")

	rec := doUpload(t, h, proj, "cosmo", "amd64", "", "not-an-ape")
	require.Equal(t, http.StatusCreated, rec.Code)

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 3)
}

// TestUploadArtifact_AliasRejectsWindowsOnStubPE covers the failure that is
// invisible downstream: a stub PE header starts on Windows and exits 0 without
// running, which every consumer reads as success.
func TestUploadArtifact_AliasRejectsWindowsOnStubPE(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "stubpealiasproj")

	rec := doUpload(t, h, proj, "cosmo", "amd64", "", apeWithPESections(t, 1))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, decodeError(t, rec), "do-nothing stub")

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Empty(t, rows, "a rejected claim must create nothing")
}

// TestUploadArtifact_StubPEWithoutWindowsIsFine pins the gate's scope: the stub
// header only falsifies a WINDOWS claim, so a linux+darwin APE publishes.
func TestUploadArtifact_StubPEWithoutWindowsIsFine(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "stubpenowinproj")

	rec := doUpload(t, h, proj, "linux,darwin", "arm64", "", apeWithPESections(t, 1))
	require.Equal(t, http.StatusCreated, rec.Code)

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
}

func platformStrings(platforms []db.Platform) []string {
	out := make([]string, len(platforms))
	for i, p := range platforms {
		out[i] = string(p.OS) + "/" + string(p.Arch)
	}
	return out
}
