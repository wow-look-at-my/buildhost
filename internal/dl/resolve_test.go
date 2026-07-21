package dl

// Version/branch resolution tests for the download redirect: latest and
// per-branch resolution and cache-control semantics (the signed-token and
// platform-alias tests live in handler_signed_test.go).

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestDownload_LatestVersion(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel1 := seedRelease(t, d, proj.ID, "1.0.0", "master", true)
	seedRelease(t, d, proj.ID, "2.0.0", "feature-x", true)
	seedArtifact(t, d, store, rel1.ID, "linux", "amd64", "v1-binary")

	// No ?v= and no ?branch= -> resolves latest on master, not the newest
	// feature-branch version.
	req := makeRequest("myapp", url.Values{"os": {"linux"}, "arch": {"amd64"}})
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

func TestDownload_ReleaseNotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "myapp", false)

	req := makeRequest("myapp", url.Values{"v": {"9.9.9"}, "os": {"linux"}, "arch": {"amd64"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDownload_ArtifactNotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)

	req := makeRequest("myapp", url.Values{"v": {"1.0.0"}, "os": {"linux"}, "arch": {"amd64"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	// dl handler only resolves release/version, then redirects; artifact
	// resolution now happens at /static.
	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("project"))
	assert.Equal(t, "1.0.0", q.Get("v"))
	assert.Equal(t, "linux", q.Get("os"))
	assert.Equal(t, "amd64", q.Get("arch"))
	assert.Equal(t, "raw", q.Get("fmt"))
}

// Note: Private project auth (unauthorized, wrong token, etc.) is tested via
// requireProject middleware in the auth package.

func TestDownload_Latest_Success(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "master", true)
	rel2 := seedRelease(t, d, proj.ID, "2.0.0", "master", true)
	seedArtifact(t, d, store, rel2.ID, "darwin", "arm64", "latest-darwin-binary")

	// No ?v= and no ?branch= -> resolves latest
	req := makeRequest("myapp", url.Values{"os": {"darwin"}, "arch": {"arm64"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("project"))
	assert.Equal(t, "2.0.0", q.Get("v"))
	assert.Equal(t, "darwin", q.Get("os"))
	assert.Equal(t, "arm64", q.Get("arch"))
	assert.Equal(t, "raw", q.Get("fmt"))
}

func TestDownload_Latest_NoPublishedReleases(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	// Create an unpublished release.
	seedRelease(t, d, proj.ID, "1.0.0-rc1", "master", false)

	// No ?v= and no ?branch= -> resolves latest
	req := makeRequest("myapp", url.Values{"os": {"linux"}, "arch": {"amd64"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDownload_Branch_Success(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	rel := seedRelease(t, d, proj.ID, "1.1.0-dev", "feature-x", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "feature-branch-binary")

	req := makeRequest("myapp", url.Values{"branch": {"feature-x"}, "os": {"linux"}, "arch": {"amd64"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("project"))
	assert.Equal(t, "1.1.0-dev", q.Get("v"))
	assert.Equal(t, "linux", q.Get("os"))
	assert.Equal(t, "amd64", q.Get("arch"))
	assert.Equal(t, "raw", q.Get("fmt"))
}

func TestDownload_Branch_BranchNotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)

	req := makeRequest("myapp", url.Values{"branch": {"nonexistent-branch"}, "os": {"linux"}, "arch": {"amd64"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDownload_Branch_ResolvesLatestOnBranch(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedRelease(t, d, proj.ID, "1.1.0", "main", true)
	rel3 := seedRelease(t, d, proj.ID, "1.2.0", "main", true)
	seedArtifact(t, d, store, rel3.ID, "linux", "amd64", "latest-main-binary")

	req := makeRequest("myapp", url.Values{"branch": {"main"}, "os": {"linux"}, "arch": {"amd64"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "myapp", q.Get("project"))
	assert.Equal(t, "1.2.0", q.Get("v"))
	assert.Equal(t, "linux", q.Get("os"))
	assert.Equal(t, "amd64", q.Get("arch"))
	assert.Equal(t, "raw", q.Get("fmt"))
}

func TestDownload_MissingOSAndArch(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	seedRelease(t, d, proj.ID, "1.0.0", "main", true)

	req := makeRequest("myapp", url.Values{"v": {"1.0.0"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDownload_Latest_NoStore(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", db.LatestBranch, true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "bin")

	// "latest" is a mutable pointer: the redirect must not be cached.
	req := makeRequest("myapp", url.Values{"os": {"linux"}, "arch": {"amd64"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}

func TestDownload_ExactVersion_Immutable(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "bin")

	// An exact version is immutable: the redirect itself is safe to cache forever.
	req := makeRequest("myapp", url.Values{"v": {"1.0.0"}, "os": {"linux"}, "arch": {"amd64"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Contains(t, rec.Header().Get("Cache-Control"), "immutable")
}
