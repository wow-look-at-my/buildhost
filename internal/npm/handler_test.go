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

	h := &Handler{DB: d, Store: store, BaseURL: "http://localhost:8080", Gen: repackage.NewGenerator(store, d, "http://localhost:8080")}
	return h, d, store
}

func withRoute(r *http.Request, project *model.Project, rt route) *http.Request {
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
		wantTar  bool
		wantFile string
	}{
		// Package info: simple names
		{"simple", "/npm/@buildhost/myapp", "myapp", "", false, ""},
		{"numeric", "/npm/@buildhost/app123", "app123", "", false, ""},
		{"dotted", "/npm/@buildhost/my.app", "my.app", "", false, ""},

		// Package info: hyphenated project names
		{"hyphenated", "/npm/@buildhost/go-toolchain", "go-toolchain", "", false, ""},
		{"multi-hyphen", "/npm/@buildhost/my-cool-app", "my-cool-app", "", false, ""},
		{"many-hyphens", "/npm/@buildhost/a-b-c-d-e", "a-b-c-d-e", "", false, ""},

		// Package info: platform packages
		{"platform linux-x64", "/npm/@buildhost/go-toolchain-linux-x64", "go-toolchain", "linux-x64", false, ""},
		{"platform darwin-arm64", "/npm/@buildhost/go-toolchain-darwin-arm64", "go-toolchain", "darwin-arm64", false, ""},
		{"platform win32-x64", "/npm/@buildhost/myapp-win32-x64", "myapp", "win32-x64", false, ""},
		{"platform linux-arm64", "/npm/@buildhost/myapp-linux-arm64", "myapp", "linux-arm64", false, ""},
		{"platform linux-ia32", "/npm/@buildhost/myapp-linux-ia32", "myapp", "linux-ia32", false, ""},
		{"platform darwin-x64", "/npm/@buildhost/myapp-darwin-x64", "myapp", "darwin-x64", false, ""},
		{"platform win32-arm64", "/npm/@buildhost/myapp-win32-arm64", "myapp", "win32-arm64", false, ""},

		// Package info: hyphenated scope names (non-@buildhost scopes)
		{"hyphenated scope", "/npm/@build-host/gotoolchain", "@build-host/gotoolchain", "", false, ""},
		{"hyphenated scope and project", "/npm/@build-host/go-toolchain", "@build-host/go-toolchain", "", false, ""},
		{"multi-segment scope", "/npm/@build/build-host/go-toolchain", "@build/build-host/go-toolchain", "", false, ""},

		// Tarball: simple names
		{"tarball simple", "/npm/@buildhost/myapp/-/myapp-1.0.0.tgz", "myapp", "", true, "myapp-1.0.0.tgz"},
		{"tarball prerelease", "/npm/@buildhost/myapp/-/myapp-1.0.0-rc.1.tgz", "myapp", "", true, "myapp-1.0.0-rc.1.tgz"},

		// Tarball: hyphenated project names
		{"tarball hyphenated", "/npm/@buildhost/go-toolchain/-/go-toolchain-1.0.0.tgz", "go-toolchain", "", true, "go-toolchain-1.0.0.tgz"},
		{"tarball multi-hyphen", "/npm/@buildhost/my-cool-app/-/my-cool-app-2.3.1.tgz", "my-cool-app", "", true, "my-cool-app-2.3.1.tgz"},

		// Tarball: platform packages
		{"tarball platform", "/npm/@buildhost/go-toolchain-linux-x64/-/go-toolchain-6.0.0-linux-x64.tgz", "go-toolchain", "linux-x64", true, "go-toolchain-6.0.0-linux-x64.tgz"},
		{"tarball platform darwin", "/npm/@buildhost/myapp-darwin-arm64/-/myapp-1.0.0-darwin-arm64.tgz", "myapp", "darwin-arm64", true, "myapp-1.0.0-darwin-arm64.tgz"},

		// Tarball: hyphenated scope names
		{"tarball hyphenated scope", "/npm/@build-host/gotoolchain/-/gotoolchain-1.0.0.tgz", "@build-host/gotoolchain", "", true, "gotoolchain-1.0.0.tgz"},
		{"tarball hyphenated scope+proj", "/npm/@build-host/go-toolchain/-/go-toolchain-1.0.0.tgz", "@build-host/go-toolchain", "", true, "go-toolchain-1.0.0.tgz"},

		// Unscoped names
		{"unscoped simple", "/npm/myapp", "myapp", "", false, ""},
		{"unscoped hyphenated", "/npm/build-host", "build-host", "", false, ""},
		{"unscoped multi-hyphen", "/npm/my-build-host", "my-build-host", "", false, ""},
		{"unscoped tarball", "/npm/build-host/-/build-host-1.0.0.tgz", "build-host", "", true, "build-host-1.0.0.tgz"},

		// Multiple slashes in path
		{"extra slash in scope", "/npm/@build/host/myapp", "@build/host/myapp", "", false, ""},
		{"deep path no tarball", "/npm/@build/build-host/go-toolchain", "@build/build-host/go-toolchain", "", false, ""},
		{"deep path tarball", "/npm/@build/host/myapp/-/myapp-1.0.0.tgz", "@build/host/myapp", "", true, "myapp-1.0.0.tgz"},

		// Edge cases
		{"bare scope", "/npm/@buildhost/", "", "", false, ""},
		{"tarball empty filename", "/npm/@buildhost/myapp/-/", "myapp", "", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.url, nil)
			ri := parseRoute(req).(route)
			assert.Equal(t, tt.wantProj, ri.project, "project")
			assert.Equal(t, tt.wantPlat, ri.platform, "platform")
			assert.Equal(t, tt.wantTar, ri.isTarball, "isTarball")
			assert.Equal(t, tt.wantFile, ri.filename, "filename")
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

func TestServeHTTP_PackageInfo_OptionalDependencies(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "go-toolchain", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "6.0.0", VersionNum: 6000000}
	require.NoError(t, d.CreateRelease(ctx, rel))

	for _, plat := range []struct {
		os   model.OS
		arch model.Arch
	}{
		{model.OSLinux, model.ArchAMD64},
		{model.OSDarwin, model.ArchARM64},
	} {
		bk, bs, err := store.Put(ctx, strings.NewReader("bin-"+string(plat.os)))
		require.NoError(t, err)
		require.NoError(t, d.CreateArtifact(ctx, &model.Artifact{
			ReleaseID: rel.ID, OS: plat.os, Arch: plat.arch,
			Kind: model.KindBinary, StorageKey: bk, Size: bs, SHA256: bk,
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

	dist := v["dist"].(map[string]any)
	assert.Contains(t, dist["tarball"], "/npm/@buildhost/go-toolchain/-/go-toolchain-6.0.0.tgz")
}

func TestServeHTTP_PlatformPackageInfo(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "go-toolchain", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "6.0.0", VersionNum: 6000000}
	require.NoError(t, d.CreateRelease(ctx, rel))

	bk, bs, err := store.Put(ctx, strings.NewReader("bin"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &model.Artifact{
		ReleaseID: rel.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindBinary, StorageKey: bk, Size: bs, SHA256: bk,
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
	assert.Contains(t, dist["tarball"], "go-toolchain-6.0.0-linux-x64.tgz")
}

func TestServeHTTP_PlatformPackageInfo_NotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	req := httptest.NewRequest("GET", "/@buildhost/myapp-win32-ia32", nil)
	req = withRoute(req, proj, route{project: "myapp", platform: "win32-ia32"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_PlatformTarball_Success(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "go-toolchain", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "6.0.0", VersionNum: 6000000}
	require.NoError(t, d.CreateRelease(ctx, rel))

	bk, bs, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &model.Artifact{
		ReleaseID: rel.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindBinary, StorageKey: bk, Size: bs, SHA256: bk,
	}))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	req := httptest.NewRequest("GET", "/@buildhost/go-toolchain-linux-x64/-/go-toolchain-6.0.0-linux-x64.tgz", nil)
	req = withRoute(req, proj, route{project: "go-toolchain", platform: "linux-x64", isTarball: true, filename: "go-toolchain-6.0.0-linux-x64.tgz"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Body.Bytes())
}

func TestServeHTTP_BasePackageWrapperTarball(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "go-toolchain", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "6.0.0", VersionNum: 6000000}
	require.NoError(t, d.CreateRelease(ctx, rel))

	bk, bs, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &model.Artifact{
		ReleaseID: rel.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindBinary, StorageKey: bk, Size: bs, SHA256: bk,
	}))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	// The base package tarball URL (from metadata) should serve a generated wrapper
	req := httptest.NewRequest("GET", "/@buildhost/go-toolchain/-/go-toolchain-6.0.0.tgz", nil)
	req = withRoute(req, proj, route{project: "go-toolchain", isTarball: true, filename: "go-toolchain-6.0.0.tgz"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	assert.Greater(t, rec.Body.Len(), 0)
}

func TestServeHTTP_WrapperTarball_NonexistentVersion(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))

	bk, bs, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &model.Artifact{
		ReleaseID: rel.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindBinary, StorageKey: bk, Size: bs, SHA256: bk,
	}))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	req := httptest.NewRequest("GET", "/@buildhost/myapp/-/myapp-99.99.99.tgz", nil)
	req = withRoute(req, proj, route{project: "myapp", isTarball: true, filename: "myapp-99.99.99.tgz"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_Tarball_NotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "nonexistent", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/@buildhost/nonexistent-linux-x64/-/nonexistent-1.0.0-linux-x64.tgz", nil)
	req = withRoute(req, proj, route{project: "nonexistent", platform: "linux-x64", isTarball: true, filename: "nonexistent-1.0.0-linux-x64.tgz"})
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
	require.NoError(t, d.CreateArtifact(ctx, &model.Artifact{
		ReleaseID: rel.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindBinary, StorageKey: binaryKey, Size: binarySize, SHA256: binaryKey,
	}))

	req := httptest.NewRequest("GET", "/@buildhost/myapp-linux-x64/-/myapp-1.0.0-linux-x64.tgz", nil)
	req = withRoute(req, proj, route{project: "myapp", platform: "linux-x64", isTarball: true, filename: "myapp-1.0.0-linux-x64.tgz"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Body.Bytes())
}

func TestServeHTTP_HyphenatedProject_PackageInfo(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "go-toolchain", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.2.0", VersionNum: 1002000}
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
	assert.Contains(t, dist["tarball"], "/npm/@buildhost/go-toolchain/-/go-toolchain-1.2.0.tgz")
}

// Private project auth is tested in the auth package. These tests verify
// the handler works correctly when auth context is already set up.

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
	require.NoError(t, d.CreateArtifact(ctx, &model.Artifact{
		ReleaseID: rel.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindBinary, StorageKey: binaryKey, Size: binarySize, SHA256: binaryKey,
	}))

	req := httptest.NewRequest("GET", "/@buildhost/secret-linux-x64/-/secret-1.0.0-linux-x64.tgz", nil)
	req = withRoute(req, proj, route{project: "secret", platform: "linux-x64", isTarball: true, filename: "secret-1.0.0-linux-x64.tgz"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Body.Bytes())
}

func TestExtractVersionFromFilename(t *testing.T) {
	tests := []struct {
		project  string
		filename string
		want     string
	}{
		{"myapp", "myapp-1.0.0.tgz", "1.0.0"},
		{"go-toolchain", "go-toolchain-6.0.0.tgz", "6.0.0"},
		{"myapp", "myapp-1.0.0-rc.1.tgz", "1.0.0-rc.1"},
		{"myapp", "wrong-1.0.0.tgz", ""},
		{"myapp", "myapp.tgz", ""},
		{"myapp", "myapp-1.0.0.tar.gz", ""},
	}
	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := extractVersionFromFilename(tt.project, tt.filename)
			assert.Equal(t, tt.want, got)
		})
	}
}
