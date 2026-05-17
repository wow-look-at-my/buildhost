package dl

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

func seedProject(t *testing.T, d *db.DB, name string, private bool) *model.Project {
	t.Helper()
	p := &model.Project{Name: name, IsPrivate: private, Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(context.Background(), p))
	return p
}

func seedRelease(t *testing.T, d *db.DB, projectID int64, version, branch string, published bool) *model.Release {
	t.Helper()
	num, err := d.NextVersionNum(context.Background(), projectID)
	require.NoError(t, err)
	r := &model.Release{
		ProjectID:  projectID,
		Version:    version,
		VersionNum: num,
		GitBranch:  branch,
	}
	require.NoError(t, d.CreateRelease(context.Background(), r))
	if published {
		require.NoError(t, d.PublishRelease(context.Background(), r.ID))
		r.Published = true
	}
	return r
}

func seedArtifact(t *testing.T, d *db.DB, store *storage.Filesystem, releaseID int64, os, arch, content string) *model.Artifact {
	t.Helper()
	key, size, err := store.Put(context.Background(), strings.NewReader(content))
	require.NoError(t, err)

	a := &model.Artifact{
		ReleaseID:  releaseID,
		OS:         model.OS(os),
		Arch:       model.Arch(arch),
		Kind:       model.KindBinary,
		StorageKey: key,
		Size:       size,
		SHA256:     key,
	}
	require.NoError(t, d.CreateArtifact(context.Background(), a))
	return a
}

func seedArtifactWithDebug(t *testing.T, d *db.DB, store *storage.Filesystem, releaseID int64, os, arch, content, debugContent string) *model.Artifact {
	t.Helper()
	a := seedArtifact(t, d, store, releaseID, os, arch, content)

	debugKey, debugSize, err := store.Put(context.Background(), strings.NewReader(debugContent))
	require.NoError(t, err)

	require.NoError(t, d.UpdateArtifactStripped(context.Background(), a.ID, "", 0, "", debugKey, debugSize))
	a.DebugStorageKey = debugKey
	a.DebugSize = debugSize
	return a
}

func seedArtifactWithStripped(t *testing.T, d *db.DB, store *storage.Filesystem, releaseID int64, os, arch, content, strippedContent string) *model.Artifact {
	t.Helper()
	a := seedArtifact(t, d, store, releaseID, os, arch, content)

	strippedKey, strippedSize, err := store.Put(context.Background(), strings.NewReader(strippedContent))
	require.NoError(t, err)

	require.NoError(t, d.UpdateArtifactStripped(context.Background(), a.ID, strippedKey, strippedSize, strippedKey, "", 0))
	a.StrippedStorageKey = strippedKey
	a.StrippedSize = strippedSize
	return a
}

// makeRequest creates a request with path values set using the Go 1.22+ mux pattern.
func makeDownloadRequest(project, version, os, arch string) *http.Request {
	req := httptest.NewRequest("GET", "/dl/"+project+"/"+version+"/"+os+"/"+arch, nil)
	req.SetPathValue("project", project)
	req.SetPathValue("version", version)
	req.SetPathValue("os", os)
	req.SetPathValue("arch", arch)
	return req
}

func makeLatestRequest(project, os, arch string) *http.Request {
	req := httptest.NewRequest("GET", "/dl/"+project+"/latest/"+os+"/"+arch, nil)
	req.SetPathValue("project", project)
	req.SetPathValue("os", os)
	req.SetPathValue("arch", arch)
	return req
}

func makeBranchRequest(project, branch, os, arch string) *http.Request {
	req := httptest.NewRequest("GET", "/dl/"+project+"/branch/"+branch+"/"+os+"/"+arch, nil)
	req.SetPathValue("project", project)
	req.SetPathValue("branch", branch)
	req.SetPathValue("os", os)
	req.SetPathValue("arch", arch)
	return req
}

func TestDownload_Success_RawBinary(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "binary-content-here")

	req := makeDownloadRequest("myapp", "1.0.0", "linux", "amd64")
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "binary-content-here", rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "myapp")
}

func TestDownload_Success_ServesStrippedBinary(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifactWithStripped(t, d, store, rel.ID, "linux", "amd64", "original-binary", "stripped-binary")

	req := makeDownloadRequest("myapp", "1.0.0", "linux", "amd64")
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "stripped-binary", rec.Body.String())
}

func TestDownload_Success_DebugFlag(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifactWithDebug(t, d, store, rel.ID, "linux", "amd64", "binary-content", "debug-symbols")

	req := makeDownloadRequest("myapp", "1.0.0", "linux", "amd64")
	req.URL.RawQuery = "debug=1"
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "debug-symbols", rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "myapp-1.0.0.debug")
}

func TestDownload_DebugFlag_NoDebugAvailable(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "binary-content")

	req := makeDownloadRequest("myapp", "1.0.0", "linux", "amd64")
	req.URL.RawQuery = "debug=1"
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDownload_Success_TarGzFormat(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	a := seedArtifact(t, d, store, rel.ID, "linux", "amd64", "binary-content")

	// Seed a packaged artifact for tar.gz format.
	tarContent := "fake-tar-gz-content"
	tarKey, tarSize, err := store.Put(context.Background(), strings.NewReader(tarContent))
	require.NoError(t, err)
	require.NoError(t, d.CreatePackagedArtifact(context.Background(), a.ID, "tar.gz", tarKey, tarSize, tarKey, "myapp_1.0.0_linux_amd64.tar.gz", ""))

	req := makeDownloadRequest("myapp", "1.0.0", "linux", "amd64")
	req.URL.RawQuery = "format=tar.gz"
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "fake-tar-gz-content", rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "myapp_1.0.0_linux_amd64.tar.gz")
}

func TestDownload_FormatNotAvailable(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "binary-content")

	req := makeDownloadRequest("myapp", "1.0.0", "linux", "amd64")
	req.URL.RawQuery = "format=tar.gz"
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "format not available")
}

func TestDownload_LatestVersion(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	rel2 := seedRelease(t, d, proj.ID, "2.0.0", "main", true)
	seedArtifact(t, d, store, rel2.ID, "linux", "amd64", "v2-binary")

	req := makeDownloadRequest("myapp", "latest", "linux", "amd64")
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "v2-binary", rec.Body.String())
}

func TestDownload_ProjectNotFound(t *testing.T) {
	h, _, _ := setupTest(t)

	req := makeDownloadRequest("nonexistent", "1.0.0", "linux", "amd64")
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDownload_ReleaseNotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	seedProject(t, d, "myapp", false)

	req := makeDownloadRequest("myapp", "9.9.9", "linux", "amd64")
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDownload_ArtifactNotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)

	req := makeDownloadRequest("myapp", "1.0.0", "linux", "amd64")
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDownload_PrivateProject_Unauthorized(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "secret-app", true)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "secret-binary")

	req := makeDownloadRequest("secret-app", "1.0.0", "linux", "amd64")
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDownload_PrivateProject_ValidToken(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "secret-app", true)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "secret-binary")

	tok := &model.APIToken{ID: 1, Scopes: "read"}
	ctx := auth.WithToken(context.Background(), tok)
	req := makeDownloadRequest("secret-app", "1.0.0", "linux", "amd64")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "secret-binary", rec.Body.String())
}

func TestDownload_PrivateProject_WrongProjectToken(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "secret-app", true)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "secret-binary")

	otherProjectID := int64(999)
	tok := &model.APIToken{ID: 1, Scopes: "read", ProjectID: &otherProjectID}
	ctx := auth.WithToken(context.Background(), tok)
	req := makeDownloadRequest("secret-app", "1.0.0", "linux", "amd64")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDownload_PrivateProject_TokenWithoutReadScope(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "secret-app", true)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "secret-binary")

	tok := &model.APIToken{ID: 1, Scopes: "write"}
	ctx := auth.WithToken(context.Background(), tok)
	req := makeDownloadRequest("secret-app", "1.0.0", "linux", "amd64")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDownloadLatest_Success(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	rel2 := seedRelease(t, d, proj.ID, "2.0.0", "main", true)
	seedArtifact(t, d, store, rel2.ID, "darwin", "arm64", "latest-darwin-binary")

	req := makeLatestRequest("myapp", "darwin", "arm64")
	rec := httptest.NewRecorder()
	h.DownloadLatest(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "latest-darwin-binary", rec.Body.String())
}

func TestDownloadLatest_ProjectNotFound(t *testing.T) {
	h, _, _ := setupTest(t)

	req := makeLatestRequest("nonexistent", "linux", "amd64")
	rec := httptest.NewRecorder()
	h.DownloadLatest(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDownloadLatest_NoPublishedReleases(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	// Create an unpublished release.
	seedRelease(t, d, proj.ID, "1.0.0-rc1", "main", false)

	req := makeLatestRequest("myapp", "linux", "amd64")
	rec := httptest.NewRecorder()
	h.DownloadLatest(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDownloadLatest_PrivateProject_Unauthorized(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "private-app", true)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "private-binary")

	req := makeLatestRequest("private-app", "linux", "amd64")
	rec := httptest.NewRecorder()
	h.DownloadLatest(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDownloadLatest_PrivateProject_ValidToken(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "private-app", true)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "private-binary")

	tok := &model.APIToken{ID: 1, Scopes: "read"}
	ctx := auth.WithToken(context.Background(), tok)
	req := makeLatestRequest("private-app", "linux", "amd64")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.DownloadLatest(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "private-binary", rec.Body.String())
}

func TestDownloadBranch_Success(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	rel := seedRelease(t, d, proj.ID, "1.1.0-dev", "feature-x", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "feature-branch-binary")

	req := makeBranchRequest("myapp", "feature-x", "linux", "amd64")
	rec := httptest.NewRecorder()
	h.DownloadBranch(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "feature-branch-binary", rec.Body.String())
}

func TestDownloadBranch_ProjectNotFound(t *testing.T) {
	h, _, _ := setupTest(t)

	req := makeBranchRequest("nonexistent", "main", "linux", "amd64")
	rec := httptest.NewRecorder()
	h.DownloadBranch(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDownloadBranch_BranchNotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)

	req := makeBranchRequest("myapp", "nonexistent-branch", "linux", "amd64")
	rec := httptest.NewRecorder()
	h.DownloadBranch(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDownloadBranch_PrivateProject_Unauthorized(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "private-app", true)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "private-binary")

	req := makeBranchRequest("private-app", "main", "linux", "amd64")
	rec := httptest.NewRecorder()
	h.DownloadBranch(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDownloadBranch_PrivateProject_ValidToken(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "private-app", true)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "private-binary")

	tok := &model.APIToken{ID: 1, Scopes: "read"}
	ctx := auth.WithToken(context.Background(), tok)
	req := makeBranchRequest("private-app", "main", "linux", "amd64")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.DownloadBranch(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "private-binary", rec.Body.String())
}

func TestDownloadBranch_ResolvesLatestOnBranch(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedRelease(t, d, proj.ID, "1.1.0", "main", true)
	rel3 := seedRelease(t, d, proj.ID, "1.2.0", "main", true)
	seedArtifact(t, d, store, rel3.ID, "linux", "amd64", "latest-main-binary")

	req := makeBranchRequest("myapp", "main", "linux", "amd64")
	rec := httptest.NewRecorder()
	h.DownloadBranch(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "latest-main-binary", rec.Body.String())
}
