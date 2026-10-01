package api

// Tests for single-artifact multi-platform ingest: PUT .../artifacts/ape with

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/exeformat"
)

// apeBody is a minimal stand-in for a real APE: the MZqFpD magic the format
const apeBody = "MZqFpD\x00\x00fake-ape-payload"

// apePlatformSpec is the set gosmopolitan's fat APE covers.
const apePlatformSpec = "linux/amd64,darwin/arm64,windows/amd64"

// doAPEUpload PUTs body to the multi-platform endpoint.
func doAPEUpload(t *testing.T, h *Handler, proj *db.Project, query, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT",
		"/api/v1/projects/"+proj.Name+"/releases/1.0.0/artifacts/ape"+query, strings.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
	req.SetPathValue("project", proj.Name)
	req.SetPathValue("version", "1.0.0")
	req = withProjectRoute(req, proj)
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.UploadMultiPlatformArtifact(rec, req)
	return rec
}

// decodeError reads the message out of a jsonError body, so an assertion
// matches the message the client reads rather than its JSON escaping.
func decodeError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Error
}

func decodeArtifact(t *testing.T, rec *httptest.ResponseRecorder) db.ArtifactWithPlatforms {
	t.Helper()
	var got db.ArtifactWithPlatforms
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	return got
}

func TestUploadAPE_OneRowCoversEveryPlatform(t *testing.T) {
	t.Serial()
	h, proj, rel := setupUploadTest(t, "apeproj")
	counting := &countingStore{Storage: h.Store}
	h.Store = counting

	rec := doAPEUpload(t, h, proj, "?platforms="+apePlatformSpec, apeBody,
		map[string]string{"X-Artifact-Filename": "go-toolchain"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	got := decodeArtifact(t, rec)
	assert.Equal(t, []db.Platform{
		{OS: db.OSLinux, Arch: db.ArchAMD64},
		{OS: db.OSDarwin, Arch: db.ArchARM64},
		{OS: db.OSWindows, Arch: db.ArchAMD64},
	}, got.Platforms)
	assert.Equal(t, db.OSLinux, got.OS)
	assert.Equal(t, db.ArchAMD64, got.Arch)
	assert.Equal(t, string(exeformat.APE), got.ExeFormat)
	assert.Equal(t, "go-toolchain", got.Filename)
	assert.Equal(t, 1, counting.puts)

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, got.ID, rows[0].ID)
}

// TestUploadAPE_EveryPlatformResolvesToTheSameArtifact is the resolution half:
func TestUploadAPE_EveryPlatformResolvesToTheSameArtifact(t *testing.T) {
	t.Serial()
	h, proj, rel := setupUploadTest(t, "resolveproj")
	rec := doAPEUpload(t, h, proj, "?platforms="+apePlatformSpec, apeBody, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	want := decodeArtifact(t, rec)

	for _, p := range []struct{ os, arch string }{
		{"linux", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"},
	} {
		got, err := h.DB.GetArtifact(context.Background(), rel.ID, p.os, p.arch)
		require.NoError(t, err, "%s/%s", p.os, p.arch)
		assert.Equal(t, want.ID, got.ID, "%s/%s", p.os, p.arch)
		assert.Equal(t, want.SHA256, got.SHA256, "%s/%s", p.os, p.arch)
		assert.Equal(t, want.StorageKey, got.StorageKey, "%s/%s", p.os, p.arch)
	}

	// A platform outside the declared set is still a miss.
	_, err := h.DB.GetArtifact(context.Background(), rel.ID, "linux", "arm64")
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestUploadAPE_RejectsNonAPEMultiPlatformDeclaration(t *testing.T) {
	t.Serial()
	h, proj, rel := setupUploadTest(t, "notapeproj")

	rec := doAPEUpload(t, h, proj, "?platforms="+apePlatformSpec, "\x7fELF-not-an-ape", nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, decodeError(t, rec), "Actually Portable Executable")

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

// A single-platform declaration is not a portability claim, so any file may
// take this path.
func TestUploadAPE_SinglePlatformNeedsNoAPEMagic(t *testing.T) {
	t.Serial()
	h, proj, _ := setupUploadTest(t, "singleplatproj")

	rec := doAPEUpload(t, h, proj, "?platforms=linux/amd64", "\x7fELF-plain-binary", nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	got := decodeArtifact(t, rec)
	assert.Equal(t, []db.Platform{{OS: db.OSLinux, Arch: db.ArchAMD64}}, got.Platforms)
	assert.Empty(t, got.ExeFormat)
}

func TestUploadAPE_RejectsBadPlatformSpecs(t *testing.T) {
	t.Serial()
	h, proj, rel := setupUploadTest(t, "badspecproj")

	for name, tc := range map[string]struct{ query, wantIn string }{
		"missing":        {"", "platforms is required"},
		"empty":          {"?platforms=", "platforms is required"},
		"unknown os":     {"?platforms=linux/amd64,plan9/amd64", `invalid os "plan9"`},
		"unknown arch":   {"?platforms=linux/amd64,linux/sparc", `invalid arch "sparc"`},
		"no slash":       {"?platforms=linux", `want os/arch`},
		"empty element":  {"?platforms=linux/amd64,,darwin/arm64", "empty platform"},
		"duplicate":      {"?platforms=linux/amd64,linux/amd64", `duplicate platform "linux/amd64"`},
		"alias dup":      {"?platforms=darwin/arm64,macOS/aarch64", `duplicate platform "darwin/arm64"`},
		"incoherent":     {"?platforms=linux/wasip1", "incompatible platform"},
		"docker kind":    {"?platforms=" + apePlatformSpec + "&kind=docker", "cannot be published as a multi-platform artifact"},
		"npm kind":       {"?platforms=" + apePlatformSpec + "&kind=npm-package", "cannot be published as a multi-platform artifact"},
		"unknown kind":   {"?platforms=linux/amd64&kind=nonsense", "invalid kind"},
		"trailing comma": {"?platforms=linux/amd64,", "empty platform"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := doAPEUpload(t, h, proj, tc.query, apeBody, nil)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Contains(t, decodeError(t, rec), tc.wantIn)
		})
	}

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Empty(t, rows, "a rejected spec must store nothing")
}

// A covered platform is a taken slot: a later per-platform upload for any of
// them conflicts, and nothing partial is left behind.
func TestUploadAPE_SlotConflictsBothWays(t *testing.T) {
	t.Serial()
	h, proj, rel := setupUploadTest(t, "conflictproj")

	require.Equal(t, http.StatusCreated,
		doAPEUpload(t, h, proj, "?platforms="+apePlatformSpec, apeBody, nil).Code)

	rec := doUpload(t, h, proj, "darwin", "arm64", "", "later-binary")
	assert.Equal(t, http.StatusConflict, rec.Code)

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
}

func TestUploadAPE_ConflictsWithExistingPerPlatformRow(t *testing.T) {
	t.Serial()
	h, proj, rel := setupUploadTest(t, "reverseconflictproj")

	require.Equal(t, http.StatusCreated, doUpload(t, h, proj, "windows", "amd64", "", "winbin").Code)

	rec := doAPEUpload(t, h, proj, "?platforms="+apePlatformSpec, apeBody, nil)
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, decodeError(t, rec), "windows/amd64")

	// The whole set failed: no linux/amd64 row was left behind.
	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, db.OSWindows, rows[0].OS)
}

// A hash-reference upload must reach the same format check: the referenced
// blob's own bytes decide, not the (empty) request body.
func TestUploadAPE_HashReferenceReadsTheStoredBytes(t *testing.T) {
	t.Serial()
	h, proj, _ := setupUploadTest(t, "hashrefproj")

	first := doAPEUpload(t, h, proj, "?platforms=linux/amd64", apeBody, nil)
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	sum := decodeArtifact(t, first).SHA256

	rec := doAPEUpload(t, h, proj, "?platforms=darwin/arm64,windows/amd64&upload_sha256="+sum, "", nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	got := decodeArtifact(t, rec)
	assert.Equal(t, string(exeformat.APE), got.ExeFormat)
	assert.Equal(t, sum, got.SHA256)
}

func TestUploadAPE_PublishedReleaseRejected(t *testing.T) {
	t.Serial()
	h, proj, rel := setupUploadTest(t, "publishedapeproj")
	require.NoError(t, h.DB.PublishRelease(context.Background(), rel.ID))

	rec := doAPEUpload(t, h, proj, "?platforms="+apePlatformSpec, apeBody, nil)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestUploadArtifact_SinglePlatformResponseCarriesItsPlatform(t *testing.T) {
	t.Serial()
	h, proj, _ := setupUploadTest(t, "uniformproj")

	rec := doUpload(t, h, proj, "linux", "amd64", "", "bin")
	require.Equal(t, http.StatusCreated, rec.Code)

	got := decodeArtifact(t, rec)
	assert.Equal(t, []db.Platform{{OS: db.OSLinux, Arch: db.ArchAMD64}}, got.Platforms)
}

// TestUploadAPE_RejectsWindowsOnStubPE is the same gate on the explicit
// endpoint. Both routes publish through publishMultiPlatform, so this pins that
// neither can drift away from the check.
func TestUploadAPE_RejectsWindowsOnStubPE(t *testing.T) {
	t.Serial()
	h, proj, _ := setupUploadTest(t, "apestubpeproj")

	rec := doAPEUpload(t, h, proj, "?platforms=linux/amd64,windows/amd64", apeWithPESections(t, 1), nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	msg := decodeError(t, rec)
	assert.Contains(t, msg, "do-nothing stub")
	assert.Contains(t, msg, "windows/amd64", "the error must name the platform to drop")
}

// TestUploadAPE_RealPEHeaderPublishesWindows is the negative control: the same
// upload with a real header is accepted, so the gate keys on the section count
// rather than on the presence of windows in the set.
func TestUploadAPE_RealPEHeaderPublishesWindows(t *testing.T) {
	t.Serial()
	h, proj, _ := setupUploadTest(t, "aperealpeproj")

	rec := doAPEUpload(t, h, proj, "?platforms=linux/amd64,windows/amd64", apeWithPESections(t, 3), nil)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Len(t, decodeArtifact(t, rec).Platforms, 2)
}
