package oci

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func setupTest(t *testing.T) (*Handler, *db.DB, *storage.Filesystem) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)

	h := &Handler{DB: d, Store: store, Gen: repackage.NewGenerator(store, "http://localhost:8080")}
	return h, d, store
}

// withRoute adds project and route info to the request context, simulating
// what the auth middleware does in production.
func withRoute(r *http.Request, project *model.Project, rt route) *http.Request {
	ctx := auth.WithProject(r.Context(), project)
	ctx = auth.WithRouteInfo(ctx, rt)
	return r.WithContext(ctx)
}

func TestParseRoute(t *testing.T) {
	tests := []struct {
		name string
		path string
		want route
	}{
		{
			name: "manifest, single-segment name",
			path: "/v2/buildhost/manifests/latest",
			want: route{project: "buildhost", action: "manifests", reference: "latest"},
		},
		{
			name: "manifest, dashed name",
			path: "/v2/go-toolchain/manifests/latest",
			want: route{project: "go-toolchain", action: "manifests", reference: "latest"},
		},
		{
			name: "blob, single-segment name",
			path: "/v2/buildhost/blobs/sha256:abc",
			want: route{project: "buildhost", action: "blobs", reference: "sha256:abc"},
		},
		{
			name: "manifest, multi-segment name (decoded path with literal '/')",
			path: "/v2/library/foo/manifests/latest",
			want: route{project: "library/foo", action: "manifests", reference: "latest"},
		},
		{
			name: "manifest, deeply nested multi-segment name",
			path: "/v2/team/group/proj-name/manifests/v1",
			want: route{project: "team/group/proj-name", action: "manifests", reference: "v1"},
		},
		{
			name: "blob, multi-segment name",
			path: "/v2/library/foo/blobs/sha256:def",
			want: route{project: "library/foo", action: "blobs", reference: "sha256:def"},
		},
		{
			name: "name itself contains literal 'manifests' segment, distinguished by LastIndex",
			path: "/v2/foo/manifests/bar/manifests/latest",
			want: route{project: "foo/manifests/bar", action: "manifests", reference: "latest"},
		},
		{
			name: "tags listing, multi-segment name",
			path: "/v2/library/foo/tags/list",
			want: route{project: "library/foo", action: "tags", reference: "list"},
		},
		{
			name: "bare project, single-segment",
			path: "/v2/myapp",
			want: route{project: "myapp"},
		},
		{
			name: "action only, no reference, single-segment",
			path: "/v2/myapp/manifests",
			want: route{project: "myapp", action: "manifests"},
		},
		{
			name: "action only, no reference, multi-segment name",
			path: "/v2/library/foo/manifests",
			want: route{project: "library/foo", action: "manifests"},
		},
		{
			name: "name itself contains an action keyword as final segment",
			path: "/v2/foo/manifests/blobs/sha256:abc",
			want: route{project: "foo/manifests", action: "blobs", reference: "sha256:abc"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			got := parseRoute(req).(route)
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
	assert.Equal(t, "{}\n", rec.Body.String())
}

func TestServeHTTP_UnknownAction(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/v2/myapp/tags/list", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "tags", reference: "list"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_Manifests_MissingRef(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
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

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
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

	// On-demand generation: no CreatePackagedArtifact needed.
	req := httptest.NewRequest("GET", "/v2/myapp/manifests/latest", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "manifests", reference: "latest"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Body.Bytes())
}

func TestServeHTTP_Blobs_MissingDigest(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
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

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
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

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
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

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	content := "blob-layer-content"
	key, size, err := store.Put(ctx, strings.NewReader(content))
	require.NoError(t, err)

	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.CreateArtifact(ctx, &model.Artifact{
		ReleaseID: rel.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindBinary, StorageKey: key, Size: size, SHA256: key,
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
