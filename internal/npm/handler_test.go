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

func TestServeHTTP_PackageInfo_ProjectNotFound(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("GET", "/@buildhost/nonexistent", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
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
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var info map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	versions := info["versions"].(map[string]any)
	assert.Equal(t, 0, len(versions))
}

func TestServeHTTP_Tarball_NotFound(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("GET", "/@buildhost/nonexistent/-/nonexistent-1.0.0.tgz", nil)
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
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	assert.Equal(t, tgzContent, rec.Body.String())
}

func TestServeHTTP_Tarball_BadPath(t *testing.T) {
	h, _, _ := setupTest(t)

	// Path without /-/ separator results in package info path, not tarball
	req := httptest.NewRequest("GET", "/@buildhost/myapp/something", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// This goes to servePackageInfo which will 404 on missing project.
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// --- Private project auth tests ----------------------------------------------

func TestServeHTTP_PrivateProject_PackageInfo_NoAuth(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/@buildhost/secret", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestServeHTTP_PrivateProject_PackageInfo_WrongProjectToken(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	wrongProjID := int64(999)
	tok := &model.APIToken{ID: 1, Scopes: "read", ProjectID: &wrongProjID}
	ctx2 := auth.WithToken(context.Background(), tok)
	req := httptest.NewRequest("GET", "/@buildhost/secret", nil)
	req = req.WithContext(ctx2)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestServeHTTP_PrivateProject_PackageInfo_ValidToken(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	tok := &model.APIToken{ID: 1, Scopes: "read", ProjectID: &proj.ID}
	ctx2 := auth.WithToken(context.Background(), tok)
	req := httptest.NewRequest("GET", "/@buildhost/secret", nil)
	req = req.WithContext(ctx2)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var info map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	assert.Equal(t, "@buildhost/secret", info["name"])
}

func TestServeHTTP_PrivateProject_Tarball_NoAuth(t *testing.T) {
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

	tgzKey, tgzSize, err := store.Put(ctx, strings.NewReader("fake-tgz"))
	require.NoError(t, err)
	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "npm", tgzKey, tgzSize, tgzKey, "secret-1.0.0.tgz", "{}"))

	req := httptest.NewRequest("GET", "/@buildhost/secret/-/secret-1.0.0.tgz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestServeHTTP_PrivateProject_Tarball_WrongProjectToken(t *testing.T) {
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

	tgzKey, tgzSize, err := store.Put(ctx, strings.NewReader("fake-tgz"))
	require.NoError(t, err)
	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "npm", tgzKey, tgzSize, tgzKey, "secret-1.0.0.tgz", "{}"))

	wrongProjID := int64(999)
	tok := &model.APIToken{ID: 1, Scopes: "read", ProjectID: &wrongProjID}
	ctx2 := auth.WithToken(context.Background(), tok)
	req := httptest.NewRequest("GET", "/@buildhost/secret/-/secret-1.0.0.tgz", nil)
	req = req.WithContext(ctx2)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestServeHTTP_PrivateProject_Tarball_ValidToken(t *testing.T) {
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

	tok := &model.APIToken{ID: 1, Scopes: "read", ProjectID: &proj.ID}
	ctx2 := auth.WithToken(context.Background(), tok)
	req := httptest.NewRequest("GET", "/@buildhost/secret/-/secret-1.0.0.tgz", nil)
	req = req.WithContext(ctx2)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, tgzContent, rec.Body.String())
}

func TestServeHTTP_PrivateProject_GlobalToken(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	// Global token (nil ProjectID) should be allowed.
	tok := &model.APIToken{ID: 1, Scopes: "read", ProjectID: nil}
	ctx2 := auth.WithToken(context.Background(), tok)
	req := httptest.NewRequest("GET", "/@buildhost/secret", nil)
	req = req.WithContext(ctx2)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}
