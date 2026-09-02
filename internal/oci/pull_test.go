package oci

// Pull-side serving tests: manifests (by tag, version, and digest), blobs,
// and the tags listing, against images synthesized by the repackage layer.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestServeHTTP_Manifests_MissingRef(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/v2/myapp/manifests", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "manifests"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_Manifests_NoRelease(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/v2/myapp/manifests/latest", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "manifests", reference: "latest"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_Manifests_NoOCIPackage(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000, GitBranch: db.LatestBranch}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	// On-demand generation means a manifest is generated from the binary
	req := httptest.NewRequest("GET", "/v2/myapp/manifests/latest", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "manifests", reference: "latest"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Body.Bytes())
}

func TestServeHTTP_Manifests_Success(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	publishWithOCI(t, ctx, d, store, proj, "1.0.0", 1000000)

	req := httptest.NewRequest("GET", "/v2/myapp/manifests/latest", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "manifests", reference: "latest"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Header().Get("Docker-Content-Digest"))

	var manifest map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &manifest))
	assert.Equal(t, float64(2), manifest["schemaVersion"])

	config := manifest["config"].(map[string]any)
	assert.Equal(t, "application/vnd.oci.image.config.v1+json", config["mediaType"])
	assert.Contains(t, config["digest"], "sha256:")

	layers := manifest["layers"].([]any)
	require.Len(t, layers, 2)
	for _, l := range layers {
		layer := l.(map[string]any)
		assert.Equal(t, "application/vnd.oci.image.layer.v1.tar+zstd", layer["mediaType"])
		assert.Contains(t, layer["digest"], "sha256:")
	}
}

func TestServeHTTP_Manifests_ByVersion(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	publishWithOCI(t, ctx, d, store, proj, "1.0.0", 1000000)
	publishWithOCI(t, ctx, d, store, proj, "2.0.0", 2000000)

	req := httptest.NewRequest("GET", "/v2/myapp/manifests/1.0.0", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "manifests", reference: "1.0.0"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServeHTTP_Manifests_ByDigest(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	publishWithOCI(t, ctx, d, store, proj, "1.0.0", 1000000)

	req := httptest.NewRequest("GET", "/v2/myapp/manifests/latest", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "manifests", reference: "latest"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	digest := rec.Header().Get("Docker-Content-Digest")
	require.NotEmpty(t, digest)

	req = httptest.NewRequest("GET", "/v2/myapp/manifests/"+digest, nil)
	req = withRoute(req, proj, route{project: "myapp", action: "manifests", reference: digest})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", rec.Header().Get("Content-Type"))
	assert.Equal(t, digest, rec.Header().Get("Docker-Content-Digest"))
}

func TestServeHTTP_Manifests_HEAD(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	publishWithOCI(t, ctx, d, store, proj, "1.0.0", 1000000)

	req := httptest.NewRequest("HEAD", "/v2/myapp/manifests/latest", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "manifests", reference: "latest"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Header().Get("Docker-Content-Digest"))
	assert.Empty(t, rec.Body.String())
}

func TestServeHTTP_Blobs_MissingDigest(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/v2/myapp/blobs", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "blobs"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_Blobs_InvalidDigest(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/v2/myapp/blobs/../../etc/passwd", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "blobs", reference: "../../etc/passwd"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_Blobs_NotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/v2/myapp/blobs/sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "blobs", reference: "sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_Blobs_Success(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	content := "blob-layer-content"
	key, size, err := store.Put(ctx, strings.NewReader(content))
	require.NoError(t, err)

	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	digest := "sha256:" + key
	req := httptest.NewRequest("GET", "/v2/myapp/blobs/"+digest, nil)
	req = withRoute(req, proj, route{project: "myapp", action: "blobs", reference: digest})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	assert.Equal(t, digest, rec.Header().Get("Docker-Content-Digest"))
	assert.Equal(t, content, rec.Body.String())
}

func TestServeHTTP_Blobs_HEAD(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	content := "blob-layer-content"
	key, size, err := store.Put(ctx, strings.NewReader(content))
	require.NoError(t, err)

	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	digest := "sha256:" + key
	req := httptest.NewRequest("HEAD", "/v2/myapp/blobs/"+digest, nil)
	req = withRoute(req, proj, route{project: "myapp", action: "blobs", reference: digest})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, digest, rec.Header().Get("Docker-Content-Digest"))
	assert.Empty(t, rec.Body.String())
}

func TestServeHTTP_Tags(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	publishWithOCI(t, ctx, d, store, proj, "1.0.0", 1000000)
	publishWithOCI(t, ctx, d, store, proj, "2.0.0", 2000000)

	req := httptest.NewRequest("GET", "/v2/myapp/tags/list", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "tags", reference: "list"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "myapp", resp.Name)
	assert.Contains(t, resp.Tags, "1.0.0")
	assert.Contains(t, resp.Tags, "2.0.0")
	assert.Contains(t, resp.Tags, "latest")
}

func TestServeHTTP_Tags_NoReleases(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/v2/myapp/tags/list", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "tags", reference: "list"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Tags []string `json:"tags"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Empty(t, resp.Tags)
}

func TestManifestDigestMatchesContent(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	publishWithOCI(t, ctx, d, store, proj, "1.0.0", 1000000)

	req := httptest.NewRequest("GET", "/v2/myapp/manifests/latest", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "manifests", reference: "latest"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.Bytes()
	computed := sha256.Sum256(body)
	expected := "sha256:" + hex.EncodeToString(computed[:])
	assert.Equal(t, expected, rec.Header().Get("Docker-Content-Digest"))
}
