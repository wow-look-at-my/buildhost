package apt

import (
	"context"
	"fmt"
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

func TestServeHTTP_NoSubpath(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/myapp", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: ""})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_UnknownSubpath(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/myapp/unknown/path", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "unknown/path"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeRelease(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/myapp/dists/stable/Release", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "dists/stable/Release"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/plain", rec.Header().Get("Content-Type"))
	body := rec.Body.String()
	assert.Contains(t, body, "Origin: buildhost")
	assert.Contains(t, body, "Label: myapp")
	assert.Contains(t, body, "Architectures: amd64 arm64 i386 armhf")
}

func TestServeRelease_InRelease(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/myapp/dists/stable/InRelease", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "dists/stable/InRelease"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Label: myapp")
}

func TestServePackages_NoRelease(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/myapp/dists/stable/main/binary-amd64/Packages", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "dists/stable/main/binary-amd64/Packages"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "", rec.Body.String())
}

func TestServePackages_Success(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Description: "A test app", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.2.3", VersionNum: 1002003}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &model.Artifact{
		ReleaseID: rel.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("GET", "/myapp/dists/stable/main/binary-amd64/Packages", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "dists/stable/main/binary-amd64/Packages"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "Package: myapp")
	assert.Contains(t, body, "Version: 1.2.3")
	assert.Contains(t, body, "Architecture: amd64")
}

func TestServePackages_NoArtifactForArch(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	// Only amd64 artifact.
	key, size, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &model.Artifact{
		ReleaseID: rel.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	// Request arm64 which doesn't exist.
	req := httptest.NewRequest("GET", "/myapp/dists/stable/main/binary-arm64/Packages", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "dists/stable/main/binary-arm64/Packages"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "", rec.Body.String())
}

func TestServePackages_BadArch(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	// No arch segment in the path.
	req := httptest.NewRequest("GET", "/myapp/dists/stable/main/binary-", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "dists/stable/main/binary-"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServePool_Success(t *testing.T) {
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

	req := httptest.NewRequest("GET", "/myapp/pool/myapp_1.0.0_amd64.deb", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "pool/myapp_1.0.0_amd64.deb"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/vnd.debian.binary-package", rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Body.Bytes())
}

func TestServePool_NoDebPackage(t *testing.T) {
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

	// On-demand generation means any supported format works as long as
	// there is an artifact in storage -- no packaged_artifacts row needed.
	req := httptest.NewRequest("GET", "/myapp/pool/myapp_1.0.0_amd64.deb", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "pool/myapp_1.0.0_amd64.deb"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/vnd.debian.binary-package", rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Body.Bytes())
}

func TestServePool_EmptyFilename(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/myapp/pool/", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "pool/"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestExtractDebArch(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"dists/stable/main/binary-amd64/Packages", "amd64"},
		{"dists/stable/main/binary-arm64/Packages", "arm64"},
		{"dists/stable/main/binary-i386/Packages", "i386"},
		{"dists/stable/main/binary-", ""},
		{"no-binary-here", ""},
	}

	for _, tt := range tests {
		got := extractDebArch(tt.input)
		assert.Equal(t, tt.expected, got, "extractDebArch(%q)", tt.input)
	}
}

func TestGoArchFromDeb(t *testing.T) {
	tests := []struct {
		debArch string
		goArch  string
	}{
		{"amd64", "amd64"},
		{"arm64", "arm64"},
		{"i386", "386"},
		{"armhf", "arm"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		got := goArchFromDeb(tt.debArch)
		assert.Equal(t, tt.goArch, got, "goArchFromDeb(%q)", tt.debArch)
	}
}

// --- Private project tests ---------------------------------------------------
// Note: Auth enforcement for private projects is now handled by the
// requireProject middleware (tested in the auth package). These tests verify
// that the handler works correctly for a private project when the middleware
// has already authorized the request and set up context.

func TestServeHTTP_PrivateProject_Release_WithValidContext(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: model.VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/secret/dists/stable/Release", nil)
	req = withRoute(req, proj, route{project: "secret", subPath: "dists/stable/Release"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Label: secret")
}

func TestServeHTTP_PrivateProject_Packages_WithValidContext(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: model.VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/secret/dists/stable/main/binary-amd64/Packages", nil)
	req = withRoute(req, proj, route{project: "secret", subPath: "dists/stable/main/binary-amd64/Packages"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServeHTTP_PrivateProject_Pool_WithValidContext(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: model.VersioningSemver}
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

	req := httptest.NewRequest("GET", "/secret/pool/secret_1.0.0_amd64.deb", nil)
	req = withRoute(req, proj, route{project: "secret", subPath: "pool/secret_1.0.0_amd64.deb"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/vnd.debian.binary-package", rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Body.Bytes())
}

// Suppress unused import warning.
var _ = fmt.Sprintf
