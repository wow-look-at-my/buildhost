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

	h := &Handler{DB: d, Store: store, BaseURL: "http://localhost:8080", Gen: repackage.NewGenerator(store, "http://localhost:8080")}
	return h, d, store
}

// withRoute adds project and route info to the request context, simulating
// what the auth middleware does in production.
func withRoute(r *http.Request, project *db.Project, rt route) *http.Request {
	ctx := auth.WithProject(r.Context(), project)
	ctx = auth.WithRouteInfo(ctx, rt)
	return r.WithContext(ctx)
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

func TestServeHTTP_Tarball_NotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "nonexistent", Versioning: db.VersioningSemver}
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

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	binaryKey, binarySize, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: binaryKey, Size: binarySize, SHA256: binaryKey,
	}))

	// On-demand generation: the NPM repackager generates filenames as
	// name-version-os-arch.tgz (e.g. myapp-1.0.0-linux-x64.tgz).
	req := httptest.NewRequest("GET", "/@buildhost/myapp/-/myapp-1.0.0-linux-x64.tgz", nil)
	req = withRoute(req, proj, route{project: "myapp", isTarball: true, filename: "myapp-1.0.0-linux-x64.tgz"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Body.Bytes())
}

// Note: Private project auth (unauthorized, wrong token, etc.) is tested via
// requireProject middleware in the auth package. The handler assumes auth has
// been enforced by the middleware and context is properly set up.

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

func TestServeHTTP_PrivateProject_Tarball_WithValidContext(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "secret", IsPrivate: true, Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	binaryKey, binarySize, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: binaryKey, Size: binarySize, SHA256: binaryKey,
	}))

	// On-demand generation: filename is secret-1.0.0-linux-x64.tgz.
	req := httptest.NewRequest("GET", "/@buildhost/secret/-/secret-1.0.0-linux-x64.tgz", nil)
	req = withRoute(req, proj, route{project: "secret", isTarball: true, filename: "secret-1.0.0-linux-x64.tgz"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Body.Bytes())
}
