package dl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// withRoute adds project and route info to the request context, simulating
// what the auth middleware does in production.
func withRoute(r *http.Request, project *model.Project, rt route) *http.Request {
	ctx := auth.WithProject(r.Context(), project)
	ctx = auth.WithRouteInfo(ctx, rt)
	return r.WithContext(ctx)
}

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

// requireRedirect asserts 302 with a Location containing "/static?" and returns
// the parsed query params from the redirect URL for further assertions.
func requireRedirect(t *testing.T, rec *httptest.ResponseRecorder) url.Values {
	t.Helper()
	assert.Equal(t, http.StatusFound, rec.Code)
	loc := rec.Header().Get("Location")
	require.NotEmpty(t, loc, "expected Location header on redirect")
	assert.Contains(t, loc, "/static?")
	u, err := url.Parse(loc)
	require.NoError(t, err)
	return u.Query()
}

func TestDownload_Success_RawBinary(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "binary-content-here")

	req := makeDownloadRequest("myapp", "1.0.0", "linux", "amd64")
	req = withRoute(req, proj, route{project: "myapp", version: "1.0.0", os: "linux", arch: "amd64"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("id"))
	assert.Equal(t, "1.0.0", q.Get("v"))
	assert.Equal(t, "linux", q.Get("os"))
	assert.Equal(t, "amd64", q.Get("arch"))
	assert.Equal(t, "raw", q.Get("fmt"))
}

func TestDownload_Success_RawFallsBackWhenStripFails(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "not-an-elf")

	req := makeDownloadRequest("myapp", "1.0.0", "linux", "amd64")
	req = withRoute(req, proj, route{project: "myapp", version: "1.0.0", os: "linux", arch: "amd64"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("id"))
	assert.Equal(t, "1.0.0", q.Get("v"))
	assert.Equal(t, "raw", q.Get("fmt"))
}

func TestDownload_DebugReturns404WhenStripFails(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "not-an-elf")

	req := makeDownloadRequest("myapp", "1.0.0", "linux", "amd64")
	req = withRoute(req, proj, route{project: "myapp", version: "1.0.0", os: "linux", arch: "amd64"})
	req.URL.RawQuery = "debug=1"
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("id"))
	assert.Equal(t, "1.0.0", q.Get("v"))
	assert.Equal(t, "1", q.Get("debug"))
}

func TestDownload_DebugFlag_NoDebugAvailable(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "binary-content")

	req := makeDownloadRequest("myapp", "1.0.0", "linux", "amd64")
	req = withRoute(req, proj, route{project: "myapp", version: "1.0.0", os: "linux", arch: "amd64"})
	req.URL.RawQuery = "debug=1"
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("id"))
	assert.Equal(t, "1.0.0", q.Get("v"))
	assert.Equal(t, "1", q.Get("debug"))
}

func TestDownload_Success_TarGzFormat(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "binary-content")

	req := makeDownloadRequest("myapp", "1.0.0", "linux", "amd64")
	req = withRoute(req, proj, route{project: "myapp", version: "1.0.0", os: "linux", arch: "amd64"})
	req.URL.RawQuery = "format=tar.gz"
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("id"))
	assert.Equal(t, "1.0.0", q.Get("v"))
	assert.Equal(t, "linux", q.Get("os"))
	assert.Equal(t, "amd64", q.Get("arch"))
	assert.Equal(t, "tar.gz", q.Get("fmt"))
}

func TestDownload_FormatNotAvailable(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "binary-content")

	req := makeDownloadRequest("myapp", "1.0.0", "linux", "amd64")
	req = withRoute(req, proj, route{project: "myapp", version: "1.0.0", os: "linux", arch: "amd64"})
	req.URL.RawQuery = "format=nonexistent"
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("id"))
	assert.Equal(t, "1.0.0", q.Get("v"))
	assert.Equal(t, "nonexistent", q.Get("fmt"))
}

func TestDownload_LatestVersion(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	rel2 := seedRelease(t, d, proj.ID, "2.0.0", "main", true)
	seedArtifact(t, d, store, rel2.ID, "linux", "amd64", "v2-binary")

	req := makeDownloadRequest("myapp", "latest", "linux", "amd64")
	req = withRoute(req, proj, route{project: "myapp", version: "latest", os: "linux", arch: "amd64"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("id"))
	assert.Equal(t, "2.0.0", q.Get("v"))
	assert.Equal(t, "linux", q.Get("os"))
	assert.Equal(t, "amd64", q.Get("arch"))
	assert.Equal(t, "raw", q.Get("fmt"))
}

func TestDownload_ReleaseNotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "myapp", false)

	req := makeDownloadRequest("myapp", "9.9.9", "linux", "amd64")
	req = withRoute(req, proj, route{project: "myapp", version: "9.9.9", os: "linux", arch: "amd64"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDownload_ArtifactNotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)

	req := makeDownloadRequest("myapp", "1.0.0", "linux", "amd64")
	req = withRoute(req, proj, route{project: "myapp", version: "1.0.0", os: "linux", arch: "amd64"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// Note: Private project auth (unauthorized, wrong token, etc.) is tested via
// requireProject middleware in the auth package.

func TestDownloadLatest_Success(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	rel2 := seedRelease(t, d, proj.ID, "2.0.0", "main", true)
	seedArtifact(t, d, store, rel2.ID, "darwin", "arm64", "latest-darwin-binary")

	req := makeLatestRequest("myapp", "darwin", "arm64")
	req = withRoute(req, proj, route{project: "myapp", os: "darwin", arch: "arm64"})
	rec := httptest.NewRecorder()
	h.DownloadLatest(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("id"))
	assert.Equal(t, "2.0.0", q.Get("v"))
	assert.Equal(t, "darwin", q.Get("os"))
	assert.Equal(t, "arm64", q.Get("arch"))
	assert.Equal(t, "raw", q.Get("fmt"))
}

func TestDownloadLatest_NoPublishedReleases(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	// Create an unpublished release.
	seedRelease(t, d, proj.ID, "1.0.0-rc1", "main", false)

	req := makeLatestRequest("myapp", "linux", "amd64")
	req = withRoute(req, proj, route{project: "myapp", os: "linux", arch: "amd64"})
	rec := httptest.NewRecorder()
	h.DownloadLatest(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDownloadBranch_Success(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	rel := seedRelease(t, d, proj.ID, "1.1.0-dev", "feature-x", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "feature-branch-binary")

	req := makeBranchRequest("myapp", "feature-x", "linux", "amd64")
	req = withRoute(req, proj, route{project: "myapp", branch: "feature-x", os: "linux", arch: "amd64"})
	rec := httptest.NewRecorder()
	h.DownloadBranch(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("id"))
	assert.Equal(t, "1.1.0-dev", q.Get("v"))
	assert.Equal(t, "linux", q.Get("os"))
	assert.Equal(t, "amd64", q.Get("arch"))
	assert.Equal(t, "raw", q.Get("fmt"))
}

func TestDownloadBranch_BranchNotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)

	req := makeBranchRequest("myapp", "nonexistent-branch", "linux", "amd64")
	req = withRoute(req, proj, route{project: "myapp", branch: "nonexistent-branch", os: "linux", arch: "amd64"})
	rec := httptest.NewRecorder()
	h.DownloadBranch(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDownloadBranch_ResolvesLatestOnBranch(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedRelease(t, d, proj.ID, "1.1.0", "main", true)
	rel3 := seedRelease(t, d, proj.ID, "1.2.0", "main", true)
	seedArtifact(t, d, store, rel3.ID, "linux", "amd64", "latest-main-binary")

	req := makeBranchRequest("myapp", "main", "linux", "amd64")
	req = withRoute(req, proj, route{project: "myapp", branch: "main", os: "linux", arch: "amd64"})
	rec := httptest.NewRecorder()
	h.DownloadBranch(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("id"))
	assert.Equal(t, "1.2.0", q.Get("v"))
	assert.Equal(t, "linux", q.Get("os"))
	assert.Equal(t, "amd64", q.Get("arch"))
	assert.Equal(t, "raw", q.Get("fmt"))
}
