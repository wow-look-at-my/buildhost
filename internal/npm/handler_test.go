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

	h := &Handler{DB: d, BaseURL: "http://localhost:8080"}
	return h, d, store
}

// withRoute adds project and route info to the request context, simulating
// what the auth middleware does in production.
func withRoute(r *http.Request, project *db.Project, rt route) *http.Request {
	ctx := auth.WithProject(r.Context(), project)
	ctx = auth.WithRouteInfo(ctx, rt)
	return r.WithContext(ctx)
}

func TestParseRoute(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantProj string
		wantPlat string
	}{
		{"simple", "/npm/@buildhost/myapp", "myapp", ""},
		{"numeric", "/npm/@buildhost/app123", "app123", ""},
		{"dotted", "/npm/@buildhost/my.app", "my.app", ""},
		{"hyphenated", "/npm/@buildhost/go-toolchain", "go-toolchain", ""},
		{"multi-hyphen", "/npm/@buildhost/my-cool-app", "my-cool-app", ""},
		{"many-hyphens", "/npm/@buildhost/a-b-c-d-e", "a-b-c-d-e", ""},
		{"platform linux-x64", "/npm/@buildhost/go-toolchain-linux-x64", "go-toolchain", "linux-x64"},
		{"platform darwin-arm64", "/npm/@buildhost/go-toolchain-darwin-arm64", "go-toolchain", "darwin-arm64"},
		{"platform win32-x64", "/npm/@buildhost/myapp-win32-x64", "myapp", "win32-x64"},
		{"platform linux-arm64", "/npm/@buildhost/myapp-linux-arm64", "myapp", "linux-arm64"},
		{"platform linux-ia32", "/npm/@buildhost/myapp-linux-ia32", "myapp", "linux-ia32"},
		{"platform darwin-x64", "/npm/@buildhost/myapp-darwin-x64", "myapp", "darwin-x64"},
		{"platform win32-arm64", "/npm/@buildhost/myapp-win32-arm64", "myapp", "win32-arm64"},
		{"hyphenated scope", "/npm/@build-host/gotoolchain", "@build-host/gotoolchain", ""},
		{"unscoped simple", "/npm/myapp", "myapp", ""},
		{"unscoped hyphenated", "/npm/build-host", "build-host", ""},
		{"multi-segment name", "/npm/@buildhost/library/foo", "library/foo", ""},
		{"deeply nested multi-segment", "/npm/@buildhost/team/group/proj-name", "team/group/proj-name", ""},
		{"extra slash in scope", "/npm/@build/host/myapp", "@build/host/myapp", ""},
		{"bare scope", "/npm/@buildhost/", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.url, nil)
			ri := parseRoute(req).(route)
			assert.Equal(t, tt.wantProj, ri.project, "project")
			assert.Equal(t, tt.wantPlat, ri.platform, "platform")
		})
	}
}

func TestSplitPlatform(t *testing.T) {
	tests := []struct {
		input    string
		wantProj string
		wantPlat string
	}{
		{"myapp", "myapp", ""},
		{"go-toolchain", "go-toolchain", ""},
		{"go-toolchain-linux-x64", "go-toolchain", "linux-x64"},
		{"go-toolchain-darwin-arm64", "go-toolchain", "darwin-arm64"},
		{"go-toolchain-win32-x64", "go-toolchain", "win32-x64"},
		{"my-cool-app-linux-arm64", "my-cool-app", "linux-arm64"},
		{"app-linux-ia32", "app", "linux-ia32"},
		{"a-b-c-darwin-x64", "a-b-c", "darwin-x64"},
		// Not a known platform - treated as project name
		{"myapp-freebsd-amd64", "myapp-freebsd-amd64", ""},
		{"myapp-linux", "myapp-linux", ""},
		{"myapp-x64", "myapp-x64", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			proj, plat := splitPlatform(tt.input)
			assert.Equal(t, tt.wantProj, proj, "project")
			assert.Equal(t, tt.wantPlat, plat, "platform")
		})
	}
}

func TestServeHTTP_PackageInfo_Success(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
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

	proj := &db.Project{Name: "myapp2", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	// Create unpublished release.
	require.NoError(t, d.CreateRelease(ctx, &db.Release{ProjectID: proj.ID, Version: "1.0.0-rc1", VersionNum: 1}))

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

func TestServeHTTP_PackageInfo_OptionalDependencies(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "go-toolchain", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "6.0.0", VersionNum: 6000000}
	require.NoError(t, d.CreateRelease(ctx, rel))

	for _, plat := range []struct {
		os   db.OS
		arch db.Arch
	}{
		{db.OSLinux, db.ArchAMD64},
		{db.OSDarwin, db.ArchARM64},
	} {
		bk, bs, err := store.Put(ctx, strings.NewReader("bin-"+string(plat.os)))
		require.NoError(t, err)
		require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
			ReleaseID: rel.ID, OS: plat.os, Arch: plat.arch,
			Kind: db.KindBinary, StorageKey: bk, Size: bs, SHA256: bk,
		}))
	}

	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	req := httptest.NewRequest("GET", "/@buildhost/go-toolchain", nil)
	req = withRoute(req, proj, route{project: "go-toolchain"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var info map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))

	versions := info["versions"].(map[string]any)
	v := versions["6.0.0"].(map[string]any)

	optDeps, ok := v["optionalDependencies"].(map[string]any)
	require.True(t, ok, "expected optionalDependencies")
	assert.Contains(t, optDeps, "@buildhost/go-toolchain-linux-x64")
	assert.Contains(t, optDeps, "@buildhost/go-toolchain-darwin-arm64")
	assert.Equal(t, "6.0.0", optDeps["@buildhost/go-toolchain-linux-x64"])

	bin, ok := v["bin"].(map[string]any)
	require.True(t, ok, "expected bin")
	assert.Equal(t, "./bin/run.js", bin["go-toolchain"])

	dist := v["dist"].(map[string]any)
	assert.Contains(t, dist["tarball"], "/static?arch=any&fmt=npm-wrapper&id=go-toolchain&os=any&v=6.0.0")
}

func TestServeHTTP_PlatformPackageInfo(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "go-toolchain", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "6.0.0", VersionNum: 6000000}
	require.NoError(t, d.CreateRelease(ctx, rel))

	bk, bs, err := store.Put(ctx, strings.NewReader("bin"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: bk, Size: bs, SHA256: bk,
	}))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	req := httptest.NewRequest("GET", "/@buildhost/go-toolchain-linux-x64", nil)
	req = withRoute(req, proj, route{project: "go-toolchain", platform: "linux-x64"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var info map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))

	assert.Equal(t, "@buildhost/go-toolchain-linux-x64", info["name"])
	versions := info["versions"].(map[string]any)
	v := versions["6.0.0"].(map[string]any)

	assert.Equal(t, []any{"linux"}, v["os"])
	assert.Equal(t, []any{"x64"}, v["cpu"])
	dist := v["dist"].(map[string]any)
	assert.Contains(t, dist["tarball"], "/static?arch=amd64&fmt=npm&id=go-toolchain&os=linux&v=6.0.0")
}

func TestServeHTTP_PlatformPackageInfo_NotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	req := httptest.NewRequest("GET", "/@buildhost/myapp-win32-ia32", nil)
	req = withRoute(req, proj, route{project: "myapp", platform: "win32-ia32"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_HyphenatedProject_PackageInfo(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "go-toolchain", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.2.0", VersionNum: 1002000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	req := httptest.NewRequest("GET", "/@buildhost/go-toolchain", nil)
	req = withRoute(req, proj, route{project: "go-toolchain"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var info map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	assert.Equal(t, "@buildhost/go-toolchain", info["name"])

	versions := info["versions"].(map[string]any)
	assert.Contains(t, versions, "1.2.0")
	v := versions["1.2.0"].(map[string]any)
	dist := v["dist"].(map[string]any)
	assert.Contains(t, dist["tarball"], "/static?arch=any&fmt=npm-wrapper&id=go-toolchain&os=any&v=1.2.0")
}

// Private project auth is tested in the auth package. These tests verify
// the handler works correctly when auth context is already set up.

func TestServeHTTP_PrivateProject_PackageInfo_WithValidContext(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "secret", IsPrivate: true, Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
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
