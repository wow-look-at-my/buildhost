package apt

import (
	"context"
	"fmt"
	"io"
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

func TestServeHTTP_NoSubpath(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("GET", "/myapp", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_ProjectNotFound(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("GET", "/nonexistent/dists/stable/Release", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeHTTP_UnknownSubpath(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	require.NoError(t, d.CreateProject(ctx, &model.Project{Name: "myapp", Versioning: model.VersioningSemver}))

	req := httptest.NewRequest("GET", "/myapp/unknown/path", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeRelease(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	require.NoError(t, d.CreateProject(ctx, &model.Project{Name: "myapp", Versioning: model.VersioningSemver}))

	req := httptest.NewRequest("GET", "/myapp/dists/stable/Release", nil)
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

	require.NoError(t, d.CreateProject(ctx, &model.Project{Name: "myapp", Versioning: model.VersioningSemver}))

	req := httptest.NewRequest("GET", "/myapp/dists/stable/InRelease", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Label: myapp")
}

func TestServePackages_NoRelease(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	require.NoError(t, d.CreateProject(ctx, &model.Project{Name: "myapp", Versioning: model.VersioningSemver}))

	req := httptest.NewRequest("GET", "/myapp/dists/stable/main/binary-amd64/Packages", nil)
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
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "", rec.Body.String())
}

func TestServePackages_BadArch(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	require.NoError(t, d.CreateProject(ctx, &model.Project{Name: "myapp", Versioning: model.VersioningSemver}))

	// No arch segment in the path.
	req := httptest.NewRequest("GET", "/myapp/dists/stable/main/binary-", nil)
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
	a := &model.Artifact{
		ReleaseID: rel.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	// Create deb packaged artifact.
	debContent := "!<arch>\nfake-deb-content"
	debKey, debSize, err := store.Put(ctx, strings.NewReader(debContent))
	require.NoError(t, err)
	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "deb", debKey, debSize, debKey, "myapp_1.0.0_amd64.deb", "{}"))

	req := httptest.NewRequest("GET", "/myapp/pool/myapp_1.0.0_amd64.deb", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/vnd.debian.binary-package", rec.Header().Get("Content-Type"))

	body, _ := io.ReadAll(rec.Result().Body)
	assert.Equal(t, debContent, string(body))
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

	req := httptest.NewRequest("GET", "/myapp/pool/myapp_1.0.0_amd64.deb", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServePool_EmptyFilename(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	require.NoError(t, d.CreateProject(ctx, &model.Project{Name: "myapp", Versioning: model.VersioningSemver}))

	req := httptest.NewRequest("GET", "/myapp/pool/", nil)
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

// Suppress unused import warning.
var _ = fmt.Sprintf
