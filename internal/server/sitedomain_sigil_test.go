package server_test

// The "@" ref grammar on the {project}.<site-domain> scheme: branches, commits,
// and the "~" spelling this scheme launched with. Split out of
// sitedomain_test.go, which covers the scheme's routing and DNS-label gate.

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSiteDomain_SigilGrammar(t *testing.T) {
	env := setupSiteDomain(t, siteTestDomain, primaryTestDomain, false)

	env.createProject(t, "sigil-p", false)
	env.uploadBranchSite(t, "sigil-p", "master", false, map[string]string{
		"index.html": "m-index", "a.html": "m-a", "404.html": "m-404",
	})
	env.uploadBranchSite(t, "sigil-p", "staging", false, map[string]string{
		"index.html": "s-index", "sub/x.js": "s-x",
	})
	env.uploadBranchSite(t, "sigil-p", "claude", false, map[string]string{
		"b.html": "c-b",
	})
	env.uploadBranchSite(t, "sigil-p", "claude/foo", false, map[string]string{
		"index.html": "cf-index", "c.html": "cf-c",
	})

	host := "sigil-p." + siteTestDomain

	// @<branch> serves that branch.
	resp, body := siteGet(t, env, host, "/@staging/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "s-index", body)
	resp, body = siteGet(t, env, host, "/@staging/sub/x.js")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "s-x", body)

	// Branch root without a trailing slash canonicalizes, like the classic scheme.
	resp, _ = siteGet(t, env, host, "/@staging")
	require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	assert.Equal(t, "/@staging/", resp.Header.Get("Location"))

	// @<default-branch> is non-canonical: 302 (no-store) to the bare form.
	resp, _ = siteGet(t, env, host, "/@master")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "/", resp.Header.Get("Location"))
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	resp, _ = siteGet(t, env, host, "/@master/a.html")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "/a.html", resp.Header.Get("Location"))
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))

	// Slash-named branches resolve by longest match: claude/foo wins for its own
	// paths, claude keeps everything else.
	resp, body = siteGet(t, env, host, "/@claude/foo/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "cf-index", body)
	resp, body = siteGet(t, env, host, "/@claude/foo/c.html")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "cf-c", body)
	resp, body = siteGet(t, env, host, "/@claude/b.html")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "c-b", body)

	// The 404.html fallback applies on the subdomain scheme.
	resp, body = siteGet(t, env, host, "/nope.html")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "m-404", body)

	// A bare sigil with no branch, and a branch that doesn't exist, both 404.
	resp, _ = siteGet(t, env, host, "/@")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp, _ = siteGet(t, env, host, "/@nosuchbranch/")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// A commit sha resolves on the project-subdomain scheme too: both schemes hand
// the same "<ref>[/<path>]" remainder to the one resolver, so a URL means the
// same file whichever host serves it. Unlike a branch name, a commit is never
// collapsed into the bare path -- it is the most specific spelling there is.
func TestSiteDomain_CommitRef(t *testing.T) {
	const sha = "0f1e2d3c4b5a69788796a5b4c3d2e1f001234567"
	env := setupSiteDomain(t, siteTestDomain, primaryTestDomain, false)

	env.createProject(t, "commit-p", false)
	resp := env.doFullHost(t, "PUT", "sites.test.local", "/commit-p/@master", "application/gzip",
		map[string]string{"X-Git-Commit": sha},
		bytes.NewReader(makeSiteTarGz(t, map[string]string{"index.html": "pinned", "a.css": "body{}"})), true)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	host := "commit-p." + siteTestDomain

	resp, body := siteGet(t, env, host, "/@"+sha+"/a.css")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "body{}", body)

	// The abbreviated form, and no collapse even though this commit's site IS
	// the default branch (the bare path serves the same bytes today, but the
	// commit URL promises this build specifically).
	resp, body = siteGet(t, env, host, "/@"+sha[:7]+"/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "pinned", body)

	// The branch name, by contrast, collapses into the bare path.
	resp, _ = siteGet(t, env, host, "/@master/a.css")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "/a.css", resp.Header.Get("Location"))

	resp, _ = siteGet(t, env, host, "/@abcdef1234567890/")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// The "~" sigil this scheme launched with still resolves: it 301s to the "@"
// spelling of the same URL, so every published ~ link keeps working while there
// is exactly one canonical form per file. The redirect lives in the handler, so
// a private branch is still gated before it is issued.

func TestSiteDomain_LegacySigilRedirects(t *testing.T) {
	env := setupSiteDomain(t, siteTestDomain, primaryTestDomain, false)

	env.createProject(t, "legacy-p", false)
	env.uploadBranchSite(t, "legacy-p", "master", false, map[string]string{"index.html": "m"})
	env.uploadBranchSite(t, "legacy-p", "staging", false, map[string]string{
		"index.html": "s-index", "sub/x.js": "s-x",
	})

	host := "legacy-p." + siteTestDomain

	for _, tc := range []struct{ from, to string }{
		{"/~staging/", "/@staging/"},
		{"/~staging", "/@staging"},
		{"/~staging/sub/x.js", "/@staging/sub/x.js"},
		{"/~master/", "/@master/"}, // even the non-canonical default-branch form
	} {
		resp, _ := siteGet(t, env, host, tc.from)
		require.Equalf(t, http.StatusMovedPermanently, resp.StatusCode, "GET %s", tc.from)
		assert.Equal(t, tc.to, resp.Header.Get("Location"))
	}

	// The query string survives the canonicalization.
	resp, _ := siteGet(t, env, host, "/~staging/sub/x.js?v=2")
	require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	assert.Equal(t, "/@staging/sub/x.js?v=2", resp.Header.Get("Location"))

	// A bare path is untouched -- "~" only means a branch at the path root.
	resp, body := siteGet(t, env, host, "/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "m", body)
}
