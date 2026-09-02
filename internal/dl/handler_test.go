package dl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/exeformat"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// withRoute adds project and route info to the request context, simulating
// what the auth middleware does in production.
func withRoute(r *http.Request, project *db.Project, rt route) *http.Request {
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

	require.NoError(t, err)

	h := &Handler{DB: d}
	return h, d, store
}

func seedProject(t *testing.T, d *db.DB, name string, private bool) *db.Project {
	t.Helper()
	p := &db.Project{Name: name, IsPrivate: private, Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(context.Background(), p))
	return p
}

func seedRelease(t *testing.T, d *db.DB, projectID int64, version, branch string, published bool) *db.Release {
	t.Helper()
	num, err := d.NextVersionNum(context.Background(), projectID)
	require.NoError(t, err)
	r := &db.Release{
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

func seedArtifact(t *testing.T, d *db.DB, store *storage.Filesystem, releaseID int64, os, arch, content string) *db.Artifact {
	t.Helper()
	key, size, err := store.Put(context.Background(), strings.NewReader(content))
	require.NoError(t, err)

	a := &db.Artifact{
		ReleaseID:  releaseID,
		OS:         db.OS(os),
		Arch:       db.Arch(arch),
		Kind:       db.KindBinary,
		StorageKey: key,
		Size:       size,
		SHA256:     key,
	}
	require.NoError(t, d.CreateArtifact(context.Background(), a))
	return a
}

func seedMultiPlatformArtifact(t *testing.T, d *db.DB, store *storage.Filesystem, releaseID int64, content string, platforms ...db.Platform) *db.Artifact {
	t.Helper()
	key, size, err := store.Put(context.Background(), strings.NewReader(content))
	require.NoError(t, err)

	a := &db.Artifact{
		ReleaseID:  releaseID,
		Kind:       db.KindBinary,
		StorageKey: key,
		Size:       size,
		SHA256:     key,
		ExeFormat:  string(exeformat.APE),
	}
	require.NoError(t, d.CreateMultiPlatformArtifact(context.Background(), a, platforms))
	return a
}

// makeRequest creates a GET request for the single Download handler using
// query params (?v=, ?branch=, ?os=, ?arch=, ?fmt=).
func makeRequest(project string, params url.Values) *http.Request {
	req := httptest.NewRequest("GET", "/dl/"+project, nil)
	req.SetPathValue("project", project)
	req.URL.RawQuery = params.Encode()
	return req
}

func requireRedirect(t *testing.T, rec *httptest.ResponseRecorder) url.Values {
	t.Helper()
	code := rec.Code
	assert.True(t, code == http.StatusFound || code == http.StatusMovedPermanently,
		"expected 302 or 301, got %d", code)
	loc := rec.Header().Get("Location")
	require.NotEmpty(t, loc, "expected Location header on redirect")
	assert.Contains(t, loc, "/file?")
	u, err := url.Parse(loc)
	require.NoError(t, err)
	return u.Query()
}

func TestDownload_Success_RawBinary(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", db.LatestBranch, true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "binary-content-here")

	req := makeRequest("myapp", url.Values{"v": {"1.0.0"}, "os": {"linux"}, "arch": {"amd64"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("project"))
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

	req := makeRequest("myapp", url.Values{"v": {"1.0.0"}, "os": {"linux"}, "arch": {"amd64"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("project"))
	assert.Equal(t, "1.0.0", q.Get("v"))
	assert.Equal(t, "raw", q.Get("fmt"))
}

func TestDownload_DebugReturns404WhenStripFails(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "not-an-elf")

	req := makeRequest("myapp", url.Values{"v": {"1.0.0"}, "os": {"linux"}, "arch": {"amd64"}, "debug": {"1"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("project"))
	assert.Equal(t, "1.0.0", q.Get("v"))
	assert.Equal(t, "1", q.Get("debug"))
}

func TestDownload_DebugFlag_NoDebugAvailable(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "binary-content")

	req := makeRequest("myapp", url.Values{"v": {"1.0.0"}, "os": {"linux"}, "arch": {"amd64"}, "debug": {"1"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("project"))
	assert.Equal(t, "1.0.0", q.Get("v"))
	assert.Equal(t, "1", q.Get("debug"))
}

func TestDownload_Success_TarGzFormat(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "binary-content")

	req := makeRequest("myapp", url.Values{"v": {"1.0.0"}, "os": {"linux"}, "arch": {"amd64"}, "fmt": {"tar.gz"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("project"))
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

	req := makeRequest("myapp", url.Values{"v": {"1.0.0"}, "os": {"linux"}, "arch": {"amd64"}, "fmt": {"nonexistent"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("project"))
	assert.Equal(t, "1.0.0", q.Get("v"))
	assert.Equal(t, "nonexistent", q.Get("fmt"))
}
