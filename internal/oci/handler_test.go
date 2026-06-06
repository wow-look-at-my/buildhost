package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTest(t *testing.T) (*Handler, *db.DB, *storage.Filesystem) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)

	h := &Handler{DB: d, Store: store, Gen: repackage.NewGenerator(store, d, t.TempDir())}
	h.uploads = newUploadStore(filepath.Join(t.TempDir(), "oci-uploads"), 10<<30)
	return h, d, store
}

// withRoute adds project and route info to the request context, simulating
// what the auth middleware does in production.
func withRoute(r *http.Request, project *db.Project, rt route) *http.Request {
	ctx := auth.WithProject(r.Context(), project)
	ctx = auth.WithRouteInfo(ctx, rt)
	return r.WithContext(ctx)
}

func publishWithOCI(t *testing.T, ctx context.Context, d *db.DB, store *storage.Filesystem, proj *db.Project, version string, versionNum int64) *db.Release {
	t.Helper()

	rel := &db.Release{ProjectID: proj.ID, Version: version, VersionNum: versionNum}
	require.NoError(t, d.CreateRelease(ctx, rel))

	binaryData := "#!/bin/sh\necho hello"
	key, size, err := store.Put(ctx, strings.NewReader(binaryData))
	require.NoError(t, err)

	a := &db.Artifact{
		ReleaseID:	rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind:	db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	oci := &repackage.OCI{Store: store, DB: d}
	data, err := readAll(store, ctx, key)
	require.NoError(t, err)

	out, err := oci.Repackage(ctx, repackage.Input{
		Project:	*proj,
		Release:	*rel,
		Artifact:	*a,
		Data:		data,
	})
	require.NoError(t, err)

	manifestKey, manifestSize, err := store.Put(ctx, out.Reader)
	require.NoError(t, err)
	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "oci", manifestKey, manifestSize, manifestKey, out.Filename, "{}"))

	require.NoError(t, d.PublishRelease(ctx, rel.ID))
	return rel
}

func readAll(store *storage.Filesystem, ctx context.Context, key string) ([]byte, error) {
	rc, _, err := store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	return data, err
}

func TestParseRoute(t *testing.T) {
	tests := []struct {
		name	string
		path	string
		want	route
	}{
		{
			name:	"manifest, single-segment name",
			path:	"/v2/buildhost/manifests/latest",
			want:	route{project: "buildhost", action: "manifests", reference: "latest"},
		},
		{
			name:	"manifest, dashed name",
			path:	"/v2/go-toolchain/manifests/latest",
			want:	route{project: "go-toolchain", action: "manifests", reference: "latest"},
		},
		{
			name:	"blob, single-segment name",
			path:	"/v2/buildhost/blobs/sha256:abc",
			want:	route{project: "buildhost", action: "blobs", reference: "sha256:abc"},
		},
		{
			name:	"manifest, multi-segment name (decoded path with literal '/')",
			path:	"/v2/library/foo/manifests/latest",
			want:	route{project: "library/foo", action: "manifests", reference: "latest"},
		},
		{
			name:	"manifest, deeply nested multi-segment name",
			path:	"/v2/team/group/proj-name/manifests/v1",
			want:	route{project: "team/group/proj-name", action: "manifests", reference: "v1"},
		},
		{
			name:	"blob, multi-segment name",
			path:	"/v2/library/foo/blobs/sha256:def",
			want:	route{project: "library/foo", action: "blobs", reference: "sha256:def"},
		},
		{
			name:	"name itself contains literal 'manifests' segment, distinguished by LastIndex",
			path:	"/v2/foo/manifests/bar/manifests/latest",
			want:	route{project: "foo/manifests/bar", action: "manifests", reference: "latest"},
		},
		{
			name:	"tags listing, multi-segment name",
			path:	"/v2/library/foo/tags/list",
			want:	route{project: "library/foo", action: "tags", reference: "list"},
		},
		{
			name:	"bare project, single-segment",
			path:	"/v2/myapp",
			want:	route{project: "myapp"},
		},
		{
			name:	"action only, no reference, single-segment",
			path:	"/v2/myapp/manifests",
			want:	route{project: "myapp", action: "manifests"},
		},
		{
			name:	"action only, no reference, multi-segment name",
			path:	"/v2/library/foo/manifests",
			want:	route{project: "library/foo", action: "manifests"},
		},
		{
			name:	"name itself contains an action keyword as final segment",
			path:	"/v2/foo/manifests/blobs/sha256:abc",
			want:	route{project: "foo/manifests", action: "blobs", reference: "sha256:abc"},
		},
		{
			name:	"blob upload start (no uuid)",
			path:	"/v2/myapp/blobs/uploads/",
			want:	route{project: "myapp", action: "uploads"},
		},
		{
			name:	"blob upload chunk by uuid, multi-segment name",
			path:	"/v2/library/foo/blobs/uploads/upload-123",
			want:	route{project: "library/foo", action: "uploads", reference: "upload-123"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// parseOCIPath is the pure path parser; parseRoute just trims the
			// /v2/ prefix and stamps the HTTP method onto the result.
			got := parseOCIPath(strings.TrimPrefix(tt.path, "/v2/"))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestV2Root(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("GET", "/v2/", nil)
	rec := httptest.NewRecorder()
	h.V2Root(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "registry/2.0", rec.Header().Get("Docker-Distribution-API-Version"))
	assert.Equal(t, "{}\n", rec.Body.String())
}

func TestV2Root_HEAD(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("HEAD", "/v2/", nil)
	rec := httptest.NewRecorder()
	h.V2Root(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "registry/2.0", rec.Header().Get("Docker-Distribution-API-Version"))
}

func TestServeHTTP_UnknownAction(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/v2/myapp/unknown/foo", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "unknown", reference: "foo"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), `"code":"NAME_UNKNOWN"`)
}

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
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID:	rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind:	db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	// On-demand generation means a manifest is generated from the binary
	// artifact -- no packaged_artifacts row needed.
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

	// Two layers: the shared essentials base layer (CA certs + minimal rootfs)
	// followed by the per-binary layer.
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
		ReleaseID:	rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind:	db.KindBinary, StorageKey: key, Size: size, SHA256: key,
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
		ReleaseID:	rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind:	db.KindBinary, StorageKey: key, Size: size, SHA256: key,
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
		Name	string		`json:"name"`
		Tags	[]string	`json:"tags"`
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

func TestBlobsReachableFromManifest(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	publishWithOCI(t, ctx, d, store, proj, "1.0.0", 1000000)

	// Get manifest
	req := httptest.NewRequest("GET", "/v2/myapp/manifests/latest", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "manifests", reference: "latest"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var manifest struct {
		Config	struct {
			Digest string `json:"digest"`
		}	`json:"config"`
		Layers	[]struct {
			Digest string `json:"digest"`
		}	`json:"layers"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &manifest))
	// Base (essentials) layer + per-binary layer; both must be reachable below.
	require.Len(t, manifest.Layers, 2)

	// Fetch config blob
	req = httptest.NewRequest("GET", "/v2/myapp/blobs/"+manifest.Config.Digest, nil)
	req = withRoute(req, proj, route{project: "myapp", action: "blobs", reference: manifest.Config.Digest})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var config map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &config))
	assert.Equal(t, "amd64", config["architecture"])
	assert.Equal(t, "linux", config["os"])

	// Fetch layer blob
	for _, layer := range manifest.Layers {
		req = httptest.NewRequest("GET", "/v2/myapp/blobs/"+layer.Digest, nil)
		req = withRoute(req, proj, route{project: "myapp", action: "blobs", reference: layer.Digest})
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.NotEmpty(t, rec.Body.Bytes())
	}
}
