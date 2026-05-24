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

func TestParseRoute(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantProj string
		wantTar  bool
		wantFile string
	}{
		// Package info: simple names
		{"simple package info", "/npm/@buildhost/myapp", "myapp", false, ""},
		{"numeric package info", "/npm/@buildhost/app123", "app123", false, ""},
		{"dotted package info", "/npm/@buildhost/my.app", "my.app", false, ""},

		// Package info: hyphenated project names
		{"hyphenated package info", "/npm/@buildhost/go-toolchain", "go-toolchain", false, ""},
		{"multi-hyphen package info", "/npm/@buildhost/my-cool-app", "my-cool-app", false, ""},
		{"leading-hyphen-segment", "/npm/@buildhost/a-b-c-d-e", "a-b-c-d-e", false, ""},

		// Package info: hyphenated scope names (non-@buildhost scopes)
		{"hyphenated scope", "/npm/@build-host/gotoolchain", "@build-host/gotoolchain", false, ""},
		{"hyphenated scope and project", "/npm/@build-host/go-toolchain", "@build-host/go-toolchain", false, ""},
		{"multi-segment scope", "/npm/@build/build-host/go-toolchain", "@build/build-host/go-toolchain", false, ""},

		// Tarball: simple names
		{"simple tarball", "/npm/@buildhost/myapp/-/myapp-1.0.0.tgz", "myapp", true, "myapp-1.0.0.tgz"},
		{"tarball semver prerelease", "/npm/@buildhost/myapp/-/myapp-1.0.0-rc.1.tgz", "myapp", true, "myapp-1.0.0-rc.1.tgz"},

		// Tarball: hyphenated project names
		{"hyphenated tarball", "/npm/@buildhost/go-toolchain/-/go-toolchain-1.0.0.tgz", "go-toolchain", true, "go-toolchain-1.0.0.tgz"},
		{"multi-hyphen tarball", "/npm/@buildhost/my-cool-app/-/my-cool-app-2.3.1.tgz", "my-cool-app", true, "my-cool-app-2.3.1.tgz"},

		// Tarball: hyphenated scope names
		{"hyphenated scope tarball", "/npm/@build-host/gotoolchain/-/gotoolchain-1.0.0.tgz", "@build-host/gotoolchain", true, "gotoolchain-1.0.0.tgz"},
		{"hyphenated scope and project tarball", "/npm/@build-host/go-toolchain/-/go-toolchain-1.0.0.tgz", "@build-host/go-toolchain", true, "go-toolchain-1.0.0.tgz"},

		// Unscoped names (no @ prefix)
		{"no scope simple", "/npm/myapp", "myapp", false, ""},
		{"no scope hyphenated", "/npm/build-host", "build-host", false, ""},
		{"no scope multi-hyphen", "/npm/my-build-host", "my-build-host", false, ""},
		{"no scope tarball", "/npm/build-host/-/build-host-1.0.0.tgz", "build-host", true, "build-host-1.0.0.tgz"},

		// Multiple slashes in path
		{"extra slash in scope", "/npm/@build/host/myapp", "@build/host/myapp", false, ""},
		{"deep path no tarball", "/npm/@build/build-host/go-toolchain", "@build/build-host/go-toolchain", false, ""},
		{"deep path tarball", "/npm/@build/host/myapp/-/myapp-1.0.0.tgz", "@build/host/myapp", true, "myapp-1.0.0.tgz"},
		{"triple slash", "/npm/a/b/c", "a/b/c", false, ""},
		{"triple slash tarball", "/npm/a/b/c/-/c-1.0.0.tgz", "a/b/c", true, "c-1.0.0.tgz"},

		// Edge cases
		{"bare scope", "/npm/@buildhost/", "", false, ""},
		{"tarball empty filename", "/npm/@buildhost/myapp/-/", "myapp", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.url, nil)
			ri := parseRoute(req).(route)
			assert.Equal(t, tt.wantProj, ri.project)
			assert.Equal(t, tt.wantTar, ri.isTarball)
			assert.Equal(t, tt.wantFile, ri.filename)
		})
	}
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

func TestServeHTTP_HyphenatedProject_Tarball(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "go-toolchain", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.2.0", VersionNum: 1002000}
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
	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "npm", tgzKey, tgzSize, tgzKey, "go-toolchain-1.2.0.tgz", "{}"))

	req := httptest.NewRequest("GET", "/@buildhost/go-toolchain/-/go-toolchain-1.2.0.tgz", nil)
	req = withRoute(req, proj, route{project: "go-toolchain", isTarball: true, filename: "go-toolchain-1.2.0.tgz"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
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
