package api

// Tests for the multi-platform upload fan-out: one uploaded blob published for
// several (os, arch) combinations via a comma list or an alias (cosmo/any) in
// the {os}/{arch} path segments.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// countingStore wraps a Storage and counts Put calls, proving a fan-out upload
// streams its body to storage exactly once.
type countingStore struct {
	storage.Storage
	puts int
}

func (c *countingStore) Put(ctx context.Context, r io.Reader) (string, int64, error) {
	c.puts++
	return c.Storage.Put(ctx, r)
}

// setupUploadTest creates a handler plus a project with one unpublished
// release ready to receive artifact uploads.
func setupUploadTest(t *testing.T, name string) (*Handler, *db.Project, *db.Release) {
	t.Helper()
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: name, Versioning: db.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))
	return h, proj, rel
}

// doUpload PUTs body to the artifact endpoint with the given raw {os}/{arch}
// path segments (specs may be comma lists or aliases).
func doUpload(t *testing.T, h *Handler, proj *db.Project, osSpec, archSpec, query, body string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/v1/projects/" + proj.Name + "/releases/1.0.0/artifacts/" + osSpec + "/" + archSpec + query
	req := httptest.NewRequest("PUT", url, strings.NewReader(body))
	req.SetPathValue("project", proj.Name)
	req.SetPathValue("version", "1.0.0")
	req.SetPathValue("os", osSpec)
	req.SetPathValue("arch", archSpec)
	req = withProjectRoute(req, proj)
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.UploadArtifact(rec, req)
	return rec
}

func platformsOf(artifacts []db.Artifact) []string {
	out := make([]string, len(artifacts))
	for i, a := range artifacts {
		out[i] = string(a.OS) + "/" + string(a.Arch)
	}
	return out
}

func TestUploadArtifact_CosmoAliasFanOut(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "cosmoproj")
	counting := &countingStore{Storage: h.Store}
	h.Store = counting

	rec := doUpload(t, h, proj, "cosmo", "amd64", "", "ape-binary")
	require.Equal(t, http.StatusCreated, rec.Code)

	var got []db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, []string{"linux/amd64", "darwin/amd64", "windows/amd64"}, platformsOf(got))

	// The blob was stored once; every row references that same blob.
	assert.Equal(t, 1, counting.puts)
	for _, a := range got {
		assert.Equal(t, got[0].StorageKey, a.StorageKey)
		assert.Equal(t, got[0].SHA256, a.SHA256)
		assert.Equal(t, got[0].Size, a.Size)
		assert.Equal(t, db.KindBinary, a.Kind)
		assert.NotZero(t, a.ID)
	}

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 3)
}

func TestUploadArtifact_CommaListFanOut(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "commaproj")

	// List elements go through NormalizeOS, so alias spellings work per element.
	rec := doUpload(t, h, proj, "linux,macOS", "arm64", "", "bin")
	require.Equal(t, http.StatusCreated, rec.Code)

	var got []db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, []string{"linux/arm64", "darwin/arm64"}, platformsOf(got))
	assert.Equal(t, got[0].StorageKey, got[1].StorageKey)

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
}

func TestUploadArtifact_ArchAnyMatrix(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "matrixproj")

	rec := doUpload(t, h, proj, "linux,windows", "any", "", "bin")
	require.Equal(t, http.StatusCreated, rec.Code)

	var got []db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, []string{
		"linux/amd64", "linux/arm64",
		"windows/amd64", "windows/arm64",
	}, platformsOf(got))

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 4)
}

func TestUploadArtifact_OSAnyArchAnyFullMatrix(t *testing.T) {
	h, proj, _ := setupUploadTest(t, "fullmatrix")

	rec := doUpload(t, h, proj, "any", "any", "", "bin")
	require.Equal(t, http.StatusCreated, rec.Code)

	var got []db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, []string{
		"linux/amd64", "linux/arm64",
		"darwin/amd64", "darwin/arm64",
		"windows/amd64", "windows/arm64",
	}, platformsOf(got))
}

// A single canonical os/arch keeps today's response byte-for-byte: one JSON
// object (not a one-element array) with the same fields.
func TestUploadArtifact_SingleOSResponseShapeUnchanged(t *testing.T) {
	h, proj, _ := setupUploadTest(t, "singleproj")

	rec := doUpload(t, h, proj, "linux", "amd64", "", "bin")
	require.Equal(t, http.StatusCreated, rec.Code)

	body := strings.TrimSpace(rec.Body.String())
	assert.True(t, strings.HasPrefix(body, "{"), "single-platform response must stay a JSON object, got: %s", body)

	var a db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &a))
	assert.Equal(t, db.OSLinux, a.OS)
	assert.Equal(t, db.ArchAMD64, a.Arch)
}

// A single aliased element normalizes (upload now accepts the same spellings
// the download path does) and still responds with the single-object shape.
func TestUploadArtifact_SingleAliasNormalized(t *testing.T) {
	h, proj, _ := setupUploadTest(t, "aliasproj")

	rec := doUpload(t, h, proj, "macos", "x86_64", "", "bin")
	require.Equal(t, http.StatusCreated, rec.Code)

	body := strings.TrimSpace(rec.Body.String())
	assert.True(t, strings.HasPrefix(body, "{"), "one combination must stay a JSON object")

	var a db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &a))
	assert.Equal(t, db.OSDarwin, a.OS)
	assert.Equal(t, db.ArchAMD64, a.Arch)
}

func TestUploadArtifact_InvalidListElement(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "badelem")

	rec := doUpload(t, h, proj, "linux,bados", "amd64", "", "bin")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `invalid os \"bados\"`)

	rec = doUpload(t, h, proj, "linux", "amd64,badarch", "", "bin")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `invalid arch \"badarch\"`)

	// A trailing comma is an empty element, not a silent no-op.
	rec = doUpload(t, h, proj, "linux,", "amd64", "", "bin")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid os")

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Empty(t, rows, "a rejected spec must create nothing")
}

func TestUploadArtifact_DuplicateListElement(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "dupelem")

	rec := doUpload(t, h, proj, "linux,linux", "amd64", "", "bin")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `duplicate os \"linux\"`)

	// Duplicates are detected after normalization: macos and darwin collide.
	rec = doUpload(t, h, proj, "macos,darwin", "amd64", "", "bin")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `duplicate os \"darwin\"`)

	rec = doUpload(t, h, proj, "linux", "amd64,x86_64", "", "bin")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `duplicate arch \"amd64\"`)

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

// A fan-out that collides with an existing row on ANY combination mirrors the
// single re-PUT semantics (409) per row -- and creates nothing, so the client
// can resolve the conflict and retry the identical request.
func TestUploadArtifact_MultiConflictAtomic(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "conflictproj")
	ctx := context.Background()

	key, size, err := h.Store.Put(ctx, strings.NewReader("existing"))
	require.NoError(t, err)
	require.NoError(t, h.DB.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSDarwin, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	rec := doUpload(t, h, proj, "cosmo", "amd64", "", "ape-binary")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "darwin/amd64", "the 409 must name the conflicting combination")

	rows, err := h.DB.ListArtifacts(ctx, rel.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1, "a conflicting fan-out must create no rows (all-or-nothing)")
}

// The npm-package sentinel row keeps its literal os=any/arch=any semantics:
// "any" must not fan out for that kind.
func TestUploadArtifact_NPMPackageAnySentinelUnchanged(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "npmproj")

	rec := doUpload(t, h, proj, "any", "any", "?kind=npm-package", "tarball")
	require.Equal(t, http.StatusCreated, rec.Code)

	body := strings.TrimSpace(rec.Body.String())
	assert.True(t, strings.HasPrefix(body, "{"), "npm-package response must stay a single JSON object")

	var a db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &a))
	assert.Equal(t, db.OS("any"), a.OS)
	assert.Equal(t, db.Arch("any"), a.Arch)

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
}
