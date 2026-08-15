package api

// Tests for the multi-platform upload fan-out: one uploaded blob published for
// several (os, arch) combinations via a comma list or an alias (cosmo/any) in
// the {os}/{arch} path segments.

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
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
	return doUploadH(t, h, proj, osSpec, archSpec, query, body, nil)
}

// doUploadH is doUpload with extra request headers (e.g. X-Artifact-Filename).
func doUploadH(t *testing.T, h *Handler, proj *db.Project, osSpec, archSpec, query, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/v1/projects/" + proj.Name + "/releases/1.0.0/artifacts/" + osSpec + "/" + archSpec + query
	req := httptest.NewRequest("PUT", url, strings.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
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

// --- hash-reference uploads ------------------------------------------------
// An empty-body PUT with ?upload_sha256=<hex> (and no upload_session)
// registers artifact row(s) for a blob the project already uploaded, without
// re-sending the bytes.

// TestUploadArtifact_HashRefRegistersExistingBlob is the happy path: one full
// upload, then a second slot registered by reference with its own filename.
func TestUploadArtifact_HashRefRegistersExistingBlob(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "hashref")
	counting := &countingStore{Storage: h.Store}
	h.Store = counting

	rec := doUploadH(t, h, proj, "linux", "amd64", "", "shared-bytes",
		map[string]string{"X-Artifact-Filename": "tool_linux_amd64"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var full db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &full))

	rec = doUploadH(t, h, proj, "windows", "amd64", "?upload_sha256="+full.SHA256, "",
		map[string]string{"X-Artifact-Filename": "tool_windows_amd64.exe"})
	require.Equal(t, http.StatusCreated, rec.Code)

	body := strings.TrimSpace(rec.Body.String())
	assert.True(t, strings.HasPrefix(body, "{"), "a single-combination hash-ref keeps the single-object response")

	var ref db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ref))
	assert.Equal(t, db.OSWindows, ref.OS)
	assert.Equal(t, db.ArchAMD64, ref.Arch)
	assert.Equal(t, full.StorageKey, ref.StorageKey)
	assert.Equal(t, full.SHA256, ref.SHA256)
	assert.Equal(t, full.Size, ref.Size)
	assert.NotZero(t, ref.ID)

	// Each slot's request carries its own filename (unlike fan-out, which
	// stamps one header across every row).
	assert.Equal(t, "tool_linux_amd64", full.Filename)
	assert.Equal(t, "tool_windows_amd64.exe", ref.Filename)

	assert.Equal(t, 1, counting.puts, "a hash-reference upload must never store bytes")

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
}

// A hash-reference composes with the {os}/{arch} fan-out grammar: the
// referenced blob fans out to the same combination set a full upload would.
func TestUploadArtifact_HashRefComposesWithFanOut(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "hashreffan")
	counting := &countingStore{Storage: h.Store}
	h.Store = counting

	rec := doUpload(t, h, proj, "linux", "amd64", "", "fanout-bytes")
	require.Equal(t, http.StatusCreated, rec.Code)
	var full db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &full))

	rec = doUpload(t, h, proj, "windows", "any", "?upload_sha256="+full.SHA256, "")
	require.Equal(t, http.StatusCreated, rec.Code)

	var got []db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, []string{"windows/amd64", "windows/arm64"}, platformsOf(got))
	for _, a := range got {
		assert.Equal(t, full.StorageKey, a.StorageKey)
	}

	assert.Equal(t, 1, counting.puts)
	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 3)
}

func TestUploadArtifact_HashRefUnknownBlob404(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "hashrefmiss")

	rec := doUpload(t, h, proj, "linux", "amd64", "?upload_sha256="+strings.Repeat("ab", 32), "")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestUploadArtifact_HashRefMalformedHash400(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "hashrefbad")

	for _, bad := range []string{"nothex", strings.Repeat("g", 64), strings.Repeat("ab", 31)} {
		rec := doUpload(t, h, proj, "linux", "amd64", "?upload_sha256="+bad, "")
		assert.Equalf(t, http.StatusBadRequest, rec.Code, "upload_sha256=%q", bad)
	}

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

// The same-project gate: sha256 values are public (release JSON, checksums
// files), so knowing a hash must never let another project mint a row serving
// the blob -- and the refusal must be indistinguishable from an unknown blob.
func TestUploadArtifact_HashRefCrossProject404(t *testing.T) {
	h, projA, _ := setupUploadTest(t, "hashrefowner")

	rec := doUpload(t, h, projA, "linux", "amd64", "", "private-bytes")
	require.Equal(t, http.StatusCreated, rec.Code)
	var full db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &full))

	ctx := context.Background()
	projB := &db.Project{Name: "hashrefother", Versioning: db.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, projB))
	relB := &db.Release{ProjectID: projB.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, relB))

	crossRec := doUpload(t, h, projB, "linux", "amd64", "?upload_sha256="+full.SHA256, "")
	assert.Equal(t, http.StatusNotFound, crossRec.Code)

	// Byte-identical to a genuinely unknown hash: no existence leak.
	unknownRec := doUpload(t, h, projB, "linux", "arm64", "?upload_sha256="+strings.Repeat("12", 32), "")
	assert.Equal(t, unknownRec.Code, crossRec.Code)
	assert.Equal(t, unknownRec.Body.String(), crossRec.Body.String())

	rows, err := h.DB.ListArtifacts(ctx, relB.ID)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

// A blob the store no longer holds (garbage-collected after its rows were
// evicted) must miss cleanly instead of creating a dangling row.
func TestUploadArtifact_HashRefBlobGone404(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "hashrefgone")

	rec := doUpload(t, h, proj, "linux", "amd64", "", "ephemeral")
	require.Equal(t, http.StatusCreated, rec.Code)
	var full db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &full))

	require.NoError(t, h.Store.Delete(context.Background(), full.StorageKey))

	rec = doUpload(t, h, proj, "windows", "amd64", "?upload_sha256="+full.SHA256, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1, "only the original full-upload row exists")
}

// Hash-ref conflicts mirror full-upload conflicts: 409 naming the
// combination, and a multi-combination request creates nothing.
func TestUploadArtifact_HashRefConflictAtomic(t *testing.T) {
	h, proj, rel := setupUploadTest(t, "hashrefconflict")

	rec := doUpload(t, h, proj, "linux", "amd64", "", "conflict-bytes")
	require.Equal(t, http.StatusCreated, rec.Code)
	var full db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &full))

	rec = doUpload(t, h, proj, "linux,windows", "amd64", "?upload_sha256="+full.SHA256, "")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "linux/amd64")

	rows, err := h.DB.ListArtifacts(context.Background(), rel.ID)
	require.NoError(t, err)
	assert.Len(t, rows, 1, "a conflicting hash-ref fan-out must create nothing")
}

// A request naming an upload session is a session finalize, never a hash-ref:
// there upload_sha256 keeps its integrity-check meaning. (In production the
// uploads middleware resolves the session before routing; at the handler
// level the session parameter must simply not trigger the hash-ref branch.)
func TestUploadArtifact_HashRefExcludedBySessionParam(t *testing.T) {
	h, proj, _ := setupUploadTest(t, "hashrefsession")

	rec := doUpload(t, h, proj, "linux", "amd64", "", "real-bytes")
	require.Equal(t, http.StatusCreated, rec.Code)
	var full db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &full))

	rec = doUpload(t, h, proj, "windows", "amd64",
		"?upload_session=sess-1&upload_sha256="+full.SHA256, "")
	require.Equal(t, http.StatusCreated, rec.Code)

	// The handler stored the (empty) request body -- it did not resolve the
	// referenced blob.
	var got db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	emptySum := sha256.Sum256(nil)
	assert.Equal(t, hex.EncodeToString(emptySum[:]), got.SHA256)
	assert.NotEqual(t, full.SHA256, got.SHA256)
}

// A body-carrying PUT with upload_sha256 keeps today's behavior byte for
// byte: the parameter is ignored and the body is stored as sent.
func TestUploadArtifact_HashParamWithBodyIgnored(t *testing.T) {
	h, proj, _ := setupUploadTest(t, "hashrefbody")

	rec := doUpload(t, h, proj, "linux", "amd64", "?upload_sha256="+strings.Repeat("ab", 32), "actual-bytes")
	require.Equal(t, http.StatusCreated, rec.Code)

	var got db.Artifact
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	sum := sha256.Sum256([]byte("actual-bytes"))
	assert.Equal(t, hex.EncodeToString(sum[:]), got.SHA256)
	assert.Equal(t, int64(len("actual-bytes")), got.Size)
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
