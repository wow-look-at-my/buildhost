package oci

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func digestOf(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// pushBlobMonolithic uploads a blob via POST .../blobs/uploads/?digest= and
// returns its digest.
func pushBlobMonolithic(t *testing.T, h *Handler, proj *db.Project, content []byte) string {
	t.Helper()
	digest := digestOf(content)
	req := httptest.NewRequest("POST", "/v2/"+proj.Name+"/blobs/uploads/?digest="+digest, bytes.NewReader(content))
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "monolithic blob upload")
	require.Equal(t, digest, rec.Header().Get("Docker-Content-Digest"))
	return digest
}

func putManifest(t *testing.T, h *Handler, proj *db.Project, reference, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", "/v2/"+proj.Name+"/manifests/"+reference, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req = withRoute(req, proj, route{project: proj.Name, action: "manifests", reference: reference})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func getManifest(t *testing.T, h *Handler, proj *db.Project, reference string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/v2/"+proj.Name+"/manifests/"+reference, nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "manifests", reference: reference})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const (
	mediaImageManifest = "application/vnd.oci.image.manifest.v1+json"
	mediaImageConfig   = "application/vnd.oci.image.config.v1+json"
	mediaImageLayer    = "application/vnd.oci.image.layer.v1.tar+gzip"
	mediaImageIndex    = "application/vnd.oci.image.index.v1+json"
)

// pushImage pushes a config blob, one layer, and a single-platform image
// manifest (by digest), returning the manifest bytes and its digest.
func pushImage(t *testing.T, h *Handler, proj *db.Project, osName, arch string) ([]byte, string) {
	t.Helper()
	config := map[string]any{
		"architecture": arch,
		"os":           osName,
		"rootfs":       map[string]any{"type": "layers", "diff_ids": []string{digestOf([]byte("diff-" + arch))}},
		"config":       map[string]any{"Entrypoint": []string{"/usr/bin/ollama"}, "Cmd": []string{"serve"}},
	}
	configBytes, _ := json.Marshal(config)
	configDigest := pushBlobMonolithic(t, h, proj, configBytes)

	layer := []byte("fake-layer-tarball-for-" + arch)
	layerDigest := pushBlobMonolithic(t, h, proj, layer)

	manifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     mediaImageManifest,
		"config":        map[string]any{"mediaType": mediaImageConfig, "digest": configDigest, "size": len(configBytes)},
		"layers":        []map[string]any{{"mediaType": mediaImageLayer, "digest": layerDigest, "size": len(layer)}},
	}
	manifestBytes, _ := json.Marshal(manifest)
	return manifestBytes, digestOf(manifestBytes)
}

func TestPush_BlobMonolithicAndServe(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), proj))

	content := []byte("hello-layer")
	digest := pushBlobMonolithic(t, h, proj, content)

	// HEAD reports presence (layer-skip dedup path).
	req := httptest.NewRequest("HEAD", "/v2/ollama/blobs/"+digest, nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "blobs", reference: digest})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	// GET returns the bytes.
	req = httptest.NewRequest("GET", "/v2/ollama/blobs/"+digest, nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "blobs", reference: digest})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, content, rec.Body.Bytes())
}

func TestPush_BlobChunked(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), proj))

	// POST with no digest -> session.
	req := httptest.NewRequest("POST", "/v2/ollama/blobs/uploads/", nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)
	uuid := rec.Header().Get("Docker-Upload-UUID")
	require.NotEmpty(t, uuid)

	content := []byte("chunk-a-chunk-b")
	// PATCH a chunk.
	req = httptest.NewRequest("PATCH", "/v2/ollama/blobs/uploads/"+uuid, bytes.NewReader(content))
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads", reference: uuid})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	// PUT finalizes with the digest.
	digest := digestOf(content)
	req = httptest.NewRequest("PUT", "/v2/ollama/blobs/uploads/"+uuid+"?digest="+digest, nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads", reference: uuid})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, digest, rec.Header().Get("Docker-Content-Digest"))

	ok, err := d.BlobBelongsToProject(t.Context(), proj.ID, digest[7:])
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestPush_BlobDigestMismatch(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), proj))

	wrong := digestOf([]byte("something-else"))
	req := httptest.NewRequest("POST", "/v2/ollama/blobs/uploads/?digest="+wrong, bytes.NewReader([]byte("real-content")))
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPush_SingleImageByTag(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := t.Context()
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, proj))

	manifestBytes, manifestDigest := pushImage(t, h, proj, "linux", "amd64")
	rec := putManifest(t, h, proj, "v1.0.0", mediaImageManifest, manifestBytes)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, manifestDigest, rec.Header().Get("Docker-Content-Digest"))

	// Tag resolves to the manifest digest and a published release.
	tag, err := d.GetOCITag(ctx, proj.ID, "v1.0.0")
	require.NoError(t, err)
	assert.Equal(t, manifestDigest, tag.ManifestDigest)

	rel, err := d.GetLatestRelease(ctx, proj.ID)
	require.NoError(t, err)
	arts, err := d.ListArtifacts(ctx, rel.ID)
	require.NoError(t, err)
	require.Len(t, arts, 1)
	assert.Equal(t, db.KindDocker, arts[0].Kind)
	assert.Equal(t, db.OSLinux, arts[0].OS)
	assert.Equal(t, db.ArchAMD64, arts[0].Arch)

	// GET by tag returns the stored manifest verbatim, with its real media type.
	got := getManifest(t, h, proj, "v1.0.0")
	require.Equal(t, http.StatusOK, got.Code)
	assert.Equal(t, manifestBytes, got.Body.Bytes())
	assert.Equal(t, mediaImageManifest, got.Header().Get("Content-Type"))
	assert.Equal(t, manifestDigest, got.Header().Get("Docker-Content-Digest"))

	// GET by digest also works.
	got = getManifest(t, h, proj, manifestDigest)
	require.Equal(t, http.StatusOK, got.Code)
	assert.Equal(t, manifestBytes, got.Body.Bytes())
}

func TestPush_MultiArchIndex(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := t.Context()
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, proj))

	amdManifest, amdDigest := pushImage(t, h, proj, "linux", "amd64")
	armManifest, armDigest := pushImage(t, h, proj, "linux", "arm64")
	// Children are pushed by digest first (as docker/buildx does).
	require.Equal(t, http.StatusCreated, putManifest(t, h, proj, amdDigest, mediaImageManifest, amdManifest).Code)
	require.Equal(t, http.StatusCreated, putManifest(t, h, proj, armDigest, mediaImageManifest, armManifest).Code)

	index := map[string]any{
		"schemaVersion": 2,
		"mediaType":     mediaImageIndex,
		"manifests": []map[string]any{
			{"mediaType": mediaImageManifest, "digest": amdDigest, "size": len(amdManifest), "platform": map[string]any{"os": "linux", "architecture": "amd64"}},
			{"mediaType": mediaImageManifest, "digest": armDigest, "size": len(armManifest), "platform": map[string]any{"os": "linux", "architecture": "arm64"}},
			// attestation entry with unknown platform must be ignored
			{"mediaType": mediaImageManifest, "digest": amdDigest, "size": len(amdManifest), "platform": map[string]any{"os": "unknown", "architecture": "unknown"}},
		},
	}
	indexBytes, _ := json.Marshal(index)
	indexDigest := digestOf(indexBytes)

	rec := putManifest(t, h, proj, "latest", mediaImageIndex, indexBytes)
	require.Equal(t, http.StatusCreated, rec.Code)

	rel, err := d.GetLatestRelease(ctx, proj.ID)
	require.NoError(t, err)
	arts, err := d.ListArtifacts(ctx, rel.ID)
	require.NoError(t, err)
	require.Len(t, arts, 2, "unknown-platform attestation entry should be skipped")

	// GET latest returns the index with the index media type.
	got := getManifest(t, h, proj, "latest")
	require.Equal(t, http.StatusOK, got.Code)
	assert.Equal(t, mediaImageIndex, got.Header().Get("Content-Type"))
	assert.Equal(t, indexDigest, got.Header().Get("Docker-Content-Digest"))
	assert.Equal(t, indexBytes, got.Body.Bytes())
}

func TestPush_RepushTag(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := t.Context()
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, proj))

	m1, d1 := pushImage(t, h, proj, "linux", "amd64")
	require.Equal(t, http.StatusCreated, putManifest(t, h, proj, "latest", mediaImageManifest, m1).Code)
	tag1, _ := d.GetOCITag(ctx, proj.ID, "latest")

	// Identical re-push is a no-op pointing at the same digest.
	require.Equal(t, http.StatusCreated, putManifest(t, h, proj, "latest", mediaImageManifest, m1).Code)
	tag1b, _ := d.GetOCITag(ctx, proj.ID, "latest")
	assert.Equal(t, tag1.ReleaseID, tag1b.ReleaseID, "identical re-push should not create a new release")

	// A different image re-tags latest to a new release; old digest still pullable.
	m2, d2 := pushImage(t, h, proj, "linux", "arm64")
	require.NotEqual(t, d1, d2)
	require.Equal(t, http.StatusCreated, putManifest(t, h, proj, "latest", mediaImageManifest, m2).Code)
	tag2, _ := d.GetOCITag(ctx, proj.ID, "latest")
	assert.Equal(t, d2, tag2.ManifestDigest)
	assert.NotEqual(t, tag1.ReleaseID, tag2.ReleaseID, "new image should create a new release")

	got := getManifest(t, h, proj, d1)
	assert.Equal(t, http.StatusOK, got.Code, "old digest remains pullable")
}

func TestPush_TagsListIncludesDockerTags(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := t.Context()
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, proj))

	m1, _ := pushImage(t, h, proj, "linux", "amd64")
	require.Equal(t, http.StatusCreated, putManifest(t, h, proj, "v1.0.0", mediaImageManifest, m1).Code)
	require.Equal(t, http.StatusCreated, putManifest(t, h, proj, "latest", mediaImageManifest, m1).Code)

	req := httptest.NewRequest("GET", "/v2/ollama/tags/list", nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "tags", reference: "list"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Tags []string `json:"tags"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Contains(t, resp.Tags, "v1.0.0")
	assert.Contains(t, resp.Tags, "latest")
}

func TestPush_BlobTooLarge(t *testing.T) {
	h, d, store := setupTest(t)
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), proj))

	// Tiny cap to exercise the limit without large data.
	h.uploads = newUploadStore(filepath.Join(t.TempDir(), "small"), 8)
	_ = store

	content := []byte("this is definitely more than eight bytes")
	req := httptest.NewRequest("POST", "/v2/ollama/blobs/uploads/?digest="+digestOf(content), bytes.NewReader(content))
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestPush_StartInvalidDigest(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), proj))

	req := httptest.NewRequest("POST", "/v2/ollama/blobs/uploads/?digest=not-a-digest", bytes.NewReader([]byte("x")))
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPush_PatchUnknownUpload(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), proj))

	req := httptest.NewRequest("PATCH", "/v2/ollama/blobs/uploads/nope", bytes.NewReader([]byte("x")))
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads", reference: "nope"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPush_PutUnknownUpload(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), proj))

	req := httptest.NewRequest("PUT", "/v2/ollama/blobs/uploads/nope?digest="+digestOf([]byte("x")), nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads", reference: "nope"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPush_PutNoDigest(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), proj))

	// Open a session.
	req := httptest.NewRequest("POST", "/v2/ollama/blobs/uploads/", nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	uuid := rec.Header().Get("Docker-Upload-UUID")
	require.NotEmpty(t, uuid)

	// PUT without ?digest= is invalid.
	req = httptest.NewRequest("PUT", "/v2/ollama/blobs/uploads/"+uuid, nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads", reference: uuid})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPush_ManifestMissingBlob(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), proj))

	// Reference a config/layer that were never pushed.
	manifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     mediaImageManifest,
		"config":        map[string]any{"mediaType": mediaImageConfig, "digest": digestOf([]byte("missing-config")), "size": 10},
		"layers":        []map[string]any{{"mediaType": mediaImageLayer, "digest": digestOf([]byte("missing-layer")), "size": 10}},
	}
	manifestBytes, _ := json.Marshal(manifest)
	rec := putManifest(t, h, proj, "v1.0.0", mediaImageManifest, manifestBytes)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "MANIFEST_BLOB_UNKNOWN")
}
