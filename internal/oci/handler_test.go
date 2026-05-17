package oci

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func setupTest(t *testing.T) (*Handler, *db.DB, *storage.Filesystem) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(t.TempDir())
	require.NoError(t, err)

	h := &Handler{DB: d, Store: store}
	return h, d, store
}

func TestServeHTTP_V2Root(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("GET", "/v2/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "{}\n", rec.Body.String())
}

func TestServeHTTP_V2RootEmpty(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("GET", "/v2", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServeHTTP_TooFewParts(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("GET", "/v2/onlyone", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_UnknownAction(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("GET", "/v2/myapp/tags/list", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_Manifests_MissingRef(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("GET", "/v2/myapp/manifests", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_Manifests_ProjectNotFound(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("GET", "/v2/nonexistent/manifests/latest", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_Manifests_NoRelease(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	require.NoError(t, d.CreateProject(ctx, &model.Project{Name: "myapp", Versioning: model.VersioningSemver}))

	req := httptest.NewRequest("GET", "/v2/myapp/manifests/latest", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_Manifests_NoOCIPackage(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &model.Artifact{
		ReleaseID: rel.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("GET", "/v2/myapp/manifests/latest", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_Manifests_Success(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	a := &model.Artifact{
		ReleaseID: rel.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	manifest := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`
	ociKey, ociSize, err := store.Put(ctx, strings.NewReader(manifest))
	require.NoError(t, err)
	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "oci", ociKey, ociSize, ociKey, "myapp-oci.json", "{}"))

	req := httptest.NewRequest("GET", "/v2/myapp/manifests/latest", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), "schemaVersion")
}

func TestServeHTTP_Blobs_MissingDigest(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("GET", "/v2/myapp/blobs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_Blobs_NotFound(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("GET", "/v2/myapp/blobs/sha256:deadbeef", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_Blobs_Success(t *testing.T) {
	h, _, store := setupTest(t)
	ctx := context.Background()

	content := "blob-layer-content"
	key, _, err := store.Put(ctx, strings.NewReader(content))
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/v2/myapp/blobs/sha256:"+key, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	assert.Equal(t, "sha256:"+key, rec.Header().Get("Docker-Content-Digest"))
	assert.Equal(t, content, rec.Body.String())
}
