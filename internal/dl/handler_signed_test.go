package dl

// Signed-token redirect and platform-alias tests. Split from
// handler_test.go, which holds the core download/resolution tests and the
// shared test helpers.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestDownload_PrivateProjectRedirectCarriesSignedToken(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "secretapp", true)
	rel := seedRelease(t, d, proj.ID, "1.0.0", db.LatestBranch, true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "bin")

	// The route is ReadAccess-gated, so reaching the handler means the caller
	req := makeRequest("secretapp", url.Values{
		"v": {"1.0.0"}, "os": {"linux"}, "arch": {"amd64"}, "fmt": {"tar.gz"},
	})
	req = withRoute(req, proj, route{project: "secretapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	// Never a permanent, never a cacheable redirect: the Location embeds a
	assert.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))

	q := requireRedirect(t, rec)
	tok := q.Get("token")
	require.NotEmpty(t, tok, "private redirect must carry a signed download token")
	assert.True(t, strings.HasPrefix(tok, "bhdl_"), "token %q must be a signed download token", tok)
	assert.True(t, auth.VerifyDownloadToken(tok, "secretapp", "1.0.0", "linux", "amd64", "tar.gz", false),
		"token must verify for the exact redirected artifact tuple")
	assert.False(t, auth.VerifyDownloadToken(tok, "secretapp", "1.0.0", "linux", "arm64", "tar.gz", false),
		"token must not verify for any other tuple")
}

func TestDownload_PrivateLatestRedirectAlsoSigned(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "secretapp", true)
	rel := seedRelease(t, d, proj.ID, "1.0.0", db.LatestBranch, true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "bin")

	req := makeRequest("secretapp", url.Values{"os": {"linux"}, "arch": {"amd64"}})
	req = withRoute(req, proj, route{project: "secretapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	assert.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	q := requireRedirect(t, rec)
	assert.True(t, auth.VerifyDownloadToken(q.Get("token"), "secretapp", "1.0.0", "linux", "amd64", "raw", false))
}

func TestDownload_PublicProjectRedirectStaysTokenFree(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", db.LatestBranch, true)
	seedArtifact(t, d, store, rel.ID, "linux", "amd64", "bin")

	req := makeRequest("myapp", url.Values{"v": {"1.0.0"}, "os": {"linux"}, "arch": {"amd64"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Empty(t, q.Get("token"), "public redirects must stay cacheable and token-free")
}

func TestDownload_NormalizesPlatformAliases(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", "main", true)
	seedArtifact(t, d, store, rel.ID, "darwin", "amd64", "bin")

	// GitHub Actions' RUNNER_OS / RUNNER_ARCH spellings must resolve natively.
	req := makeRequest("myapp", url.Values{"v": {"1.0.0"}, "os": {"macOS"}, "arch": {"X64"}})
	req = withRoute(req, proj, route{project: "myapp"})
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	q := requireRedirect(t, rec)
	assert.Equal(t, "darwin", q.Get("os"))
	assert.Equal(t, "amd64", q.Get("arch"))
}
