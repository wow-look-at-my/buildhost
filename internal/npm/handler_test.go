package npm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestParseRoute(t *testing.T) {
	tests := []struct {
		name string
		path string
		want route
	}{
		{
			name: "package info, single-word name",
			path: "/npm/@buildhost/buildhost",
			want: route{project: "buildhost"},
		},
		{
			name: "package info, dashed name",
			path: "/npm/@buildhost/go-toolchain",
			want: route{project: "go-toolchain"},
		},
		{
			name: "package info, multi-dashed name",
			path: "/npm/@buildhost/foo-bar-baz",
			want: route{project: "foo-bar-baz"},
		},
		{
			name: "tarball, single-word name",
			path: "/npm/@buildhost/buildhost/-/buildhost-1.0.0.tgz",
			want: route{project: "buildhost", isTarball: true, filename: "buildhost-1.0.0.tgz"},
		},
		{
			name: "tarball, dashed name",
			path: "/npm/@buildhost/go-toolchain/-/go-toolchain-2.0.0.tgz",
			want: route{project: "go-toolchain", isTarball: true, filename: "go-toolchain-2.0.0.tgz"},
		},
		{
			name: "tarball, multi-dashed name",
			path: "/npm/@buildhost/foo-bar-baz/-/foo-bar-baz-1.0.0.tgz",
			want: route{project: "foo-bar-baz", isTarball: true, filename: "foo-bar-baz-1.0.0.tgz"},
		},
		{
			name: "package info, name with dots",
			path: "/npm/@buildhost/foo.bar",
			want: route{project: "foo.bar"},
		},
		{
			name: "package info, name with underscore",
			path: "/npm/@buildhost/foo_bar",
			want: route{project: "foo_bar"},
		},
		{
			name: "package info, multi-segment name (slash)",
			path: "/npm/@buildhost/library/foo",
			want: route{project: "library/foo"},
		},
		{
			name: "package info, deeply nested multi-segment name",
			path: "/npm/@buildhost/team/group/proj-name",
			want: route{project: "team/group/proj-name"},
		},
		{
			name: "tarball, multi-segment name",
			path: "/npm/@buildhost/library/foo/-/foo-1.0.0.tgz",
			want: route{project: "library/foo", isTarball: true, filename: "foo-1.0.0.tgz"},
		},
		{
			name: "tarball, multi-segment name with dashed final segment",
			path: "/npm/@buildhost/team/go-toolchain/-/go-toolchain-2.0.0.tgz",
			want: route{project: "team/go-toolchain", isTarball: true, filename: "go-toolchain-2.0.0.tgz"},
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

func setupTest(t *testing.T) (*Handler, *db.DB, *storage.Filesystem) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(t.TempDir())
	require.NoError(t, err)

	h := &Handler{DB: d, Store: store, BaseURL: "http://localhost:8080"}
	return h, d, store
}

// withRoute adds project and route info to the request context, simulating
// what the auth middleware does in production.
func withRoute(r *http.Request, project *model.Project, rt route) *http.Request {
	ctx := auth.WithProject(r.Context(), project)
	ctx = auth.WithRouteInfo(ctx, rt)
	return r.WithContext(ctx)
}

func TestServeHTTP_PackageInfo_Success(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	req := httptest.NewRequest("GET", "/@buildhost/myapp", nil)
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var info map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	assert.Equal(t, "@buildhost/myapp", info["name"])
	assert.NotNil(t, info["versions"])
	assert.NotNil(t, info["dist-tags"])
}

func TestServeHTTP_PackageInfo_UnpublishedSkipped(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp2", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	// Create unpublished release.
	require.NoError(t, d.CreateRelease(ctx, &model.Release{ProjectID: proj.ID, Version: "1.0.0-rc1", VersionNum: 1}))

	req := httptest.NewRequest("GET", "/@buildhost/myapp2", nil)
	req = withRoute(req, proj, route{project: "myapp2"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var info map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	versions := info["versions"].(map[string]any)
	assert.Equal(t, 0, len(versions))
}

func TestServeHTTP_Tarball_NotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "nonexistent", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/@buildhost/nonexistent/-/nonexistent-1.0.0.tgz", nil)
	req = withRoute(req, proj, route{project: "nonexistent", isTarball: true, filename: "nonexistent-1.0.0.tgz"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_Tarball_Success(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	binaryKey, binarySize, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	a := &model.Artifact{
		ReleaseID: rel.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindBinary, StorageKey: binaryKey, Size: binarySize, SHA256: binaryKey,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	// Store npm tarball.
	tgzContent := "fake-tgz-content"
	tgzKey, tgzSize, err := store.Put(ctx, strings.NewReader(tgzContent))
	require.NoError(t, err)
	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "npm", tgzKey, tgzSize, tgzKey, "myapp-1.0.0.tgz", "{}"))

	req := httptest.NewRequest("GET", "/@buildhost/myapp/-/myapp-1.0.0.tgz", nil)
	req = withRoute(req, proj, route{project: "myapp", isTarball: true, filename: "myapp-1.0.0.tgz"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	assert.Equal(t, tgzContent, rec.Body.String())
}

// Note: Private project auth (unauthorized, wrong token, etc.) is tested via
// requireProject middleware in the auth package. The handler assumes auth has
// been enforced by the middleware and context is properly set up.

func TestServeHTTP_PrivateProject_PackageInfo_WithValidContext(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	req := httptest.NewRequest("GET", "/@buildhost/secret", nil)
	req = withRoute(req, proj, route{project: "secret"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var info map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	assert.Equal(t, "@buildhost/secret", info["name"])
}

func TestServeHTTP_PrivateProject_Tarball_WithValidContext(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	binaryKey, binarySize, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	a := &model.Artifact{
		ReleaseID: rel.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindBinary, StorageKey: binaryKey, Size: binarySize, SHA256: binaryKey,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	tgzContent := "fake-tgz-content"
	tgzKey, tgzSize, err := store.Put(ctx, strings.NewReader(tgzContent))
	require.NoError(t, err)
	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "npm", tgzKey, tgzSize, tgzKey, "secret-1.0.0.tgz", "{}"))

	req := httptest.NewRequest("GET", "/@buildhost/secret/-/secret-1.0.0.tgz", nil)
	req = withRoute(req, proj, route{project: "secret", isTarball: true, filename: "secret-1.0.0.tgz"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, tgzContent, rec.Body.String())
}
