package dl

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// apePlatforms is the set gosmopolitan's fat APE covers.
var apePlatforms = []db.Platform{
	{OS: db.OSLinux, Arch: db.ArchAMD64},
	{OS: db.OSDarwin, Arch: db.ArchARM64},
	{OS: db.OSWindows, Arch: db.ArchAMD64},
}

// redirectFor drives the dl handler for one platform and returns the static
// query it redirects to.
func redirectFor(t *testing.T, h *Handler, proj *db.Project, params url.Values) url.Values {
	t.Helper()
	req := makeRequest(proj.Name, params)
	req = withRoute(req, proj, route{project: proj.Name})
	rec := httptest.NewRecorder()
	h.Download(rec, req)
	return requireRedirect(t, rec)
}

// TestDownload_MultiPlatform_EveryPlatformGetsTheSameURL is the property the
// whole feature exists for: one uploaded APE, asked for as three different
// platforms, hands back one identical static URL -- so one CDN object, one
// digest, one ETag, and one download link in any UI listing it.
func TestDownload_MultiPlatform_EveryPlatformGetsTheSameURL(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "go-toolchain", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", db.LatestBranch, true)
	seedMultiPlatformArtifact(t, d, store, rel.ID, "MZqFpD-ape-bytes", apePlatforms...)

	var urls []string
	for _, p := range apePlatforms {
		q := redirectFor(t, h, proj, url.Values{
			"v": {"1.0.0"}, "os": {string(p.OS)}, "arch": {string(p.Arch)},
		})
		assert.Equal(t, "go-toolchain", q.Get("project"))
		// Every platform folds to the canonical slot, which is platforms[0].
		assert.Equal(t, "linux", q.Get("os"), "os for %s", p)
		assert.Equal(t, "amd64", q.Get("arch"), "arch for %s", p)
		urls = append(urls, q.Encode())
	}
	require.Len(t, urls, 3)
	assert.Equal(t, urls[0], urls[1])
	assert.Equal(t, urls[0], urls[2])
}

// TestDownload_MultiPlatform_AliasSpellingsResolve proves a consumer passing
// RUNNER_OS/uname spellings reaches the same artifact.
func TestDownload_MultiPlatform_AliasSpellingsResolve(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "go-toolchain", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", db.LatestBranch, true)
	seedMultiPlatformArtifact(t, d, store, rel.ID, "MZqFpD-ape-bytes", apePlatforms...)

	q := redirectFor(t, h, proj, url.Values{"v": {"1.0.0"}, "os": {"macOS"}, "arch": {"aarch64"}})
	assert.Equal(t, "linux", q.Get("os"))
	assert.Equal(t, "amd64", q.Get("arch"))
}

// TestDownload_MultiPlatform_LatestAndBranchResolveToo covers the two mutable
// spellings alongside the exact-version one.
func TestDownload_MultiPlatform_LatestAndBranchResolveToo(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "go-toolchain", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", db.LatestBranch, true)
	seedMultiPlatformArtifact(t, d, store, rel.ID, "MZqFpD-ape-bytes", apePlatforms...)

	for _, params := range []url.Values{
		{"os": {"windows"}, "arch": {"amd64"}},
		{"branch": {db.LatestBranch}, "os": {"darwin"}, "arch": {"arm64"}},
	} {
		q := redirectFor(t, h, proj, params)
		assert.Equal(t, "1.0.0", q.Get("v"))
		assert.Equal(t, "linux", q.Get("os"))
		assert.Equal(t, "amd64", q.Get("arch"))
	}
}

// TestDownload_MultiPlatform_UncoveredPlatformIsUntouched proves the fold is
// scoped to platforms the artifact actually covers: an uncovered pair keeps its
// own spelling and static answers the 404, exactly as before.
func TestDownload_MultiPlatform_UncoveredPlatformIsUntouched(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "go-toolchain", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", db.LatestBranch, true)
	seedMultiPlatformArtifact(t, d, store, rel.ID, "MZqFpD-ape-bytes", apePlatforms...)

	q := redirectFor(t, h, proj, url.Values{"v": {"1.0.0"}, "os": {"freebsd"}, "arch": {"amd64"}})
	assert.Equal(t, "freebsd", q.Get("os"))
	assert.Equal(t, "amd64", q.Get("arch"))
}

// TestDownload_SinglePlatform_RedirectUnchanged pins that an ordinary
// per-platform artifact still redirects to exactly its own os/arch.
func TestDownload_SinglePlatform_RedirectUnchanged(t *testing.T) {
	h, d, store := setupTest(t)
	proj := seedProject(t, d, "myapp", false)
	rel := seedRelease(t, d, proj.ID, "1.0.0", db.LatestBranch, true)
	seedArtifact(t, d, store, rel.ID, "darwin", "arm64", "binary")

	q := redirectFor(t, h, proj, url.Values{"v": {"1.0.0"}, "os": {"darwin"}, "arch": {"arm64"}})
	assert.Equal(t, "darwin", q.Get("os"))
	assert.Equal(t, "arm64", q.Get("arch"))
}
