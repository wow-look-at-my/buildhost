package sites

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A branch or commit is named with the "@" sigil: /{project}/@{ref}/{path}.
// It exists because "branch" is an ordinary path segment: a site holding a
// directory called "branch" cannot be addressed at all through the older form,
// and every URL in that form carries a segment that reads like part of the
// site. "@" is outside both the ref and the project-name charsets, so the split
// is exact.
//
// These go through the REAL router, because that is where the risk is: the "@"
// reads share the literal-less apex route, which must lose to /branch/ and
// /branches while still catching everything else.
func TestBranchSigil_Serves(t *testing.T) {
	env := setupEnv(t)
	seedProject(t, env.db, "jsperf.app")
	// master is the seed default branch, so every other branch below is a
	// non-default ref that has no shorter URL and therefore serves in place.
	env.uploadSite(t, "jsperf.app", "master", map[string]string{"index.html": "<h1>default</h1>"})
	env.uploadSite(t, "jsperf.app", "pr-7", map[string]string{
		"index.html":     "<h1>preview</h1>",
		"runner.html":    "<h1>pr runner</h1>",
		"assets/app.js":  "console.log(1)",
		"sub/index.html": "<h1>sub</h1>",
	})

	for path, want := range map[string]string{
		"/jsperf.app/@pr-7/runner.html":   "<h1>pr runner</h1>",
		"/jsperf.app/@pr-7/assets/app.js": "console.log(1)",
		"/jsperf.app/@pr-7/sub/":          "<h1>sub</h1>", // directory -> index.html
		"/jsperf.app/@pr-7/":              "<h1>preview</h1>",
	} {
		rec := env.do(t, "GET", path, "", nil, false)
		require.Equalf(t, http.StatusOK, rec.Code, "GET %s: %s", path, rec.Body.String())
		assert.Equalf(t, want, rec.Body.String(), "GET %s", path)
	}

	// A branch root without its trailing slash canonicalizes, so relative links
	// in index.html resolve under the branch -- same rule as the /branch/ form.
	rec := env.do(t, "GET", "/jsperf.app/@pr-7", "", nil, false)
	require.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/jsperf.app/@pr-7/", rec.Header().Get("Location"))

	// A ref with no site 404s; it never falls through to a same-named file.
	rec = env.do(t, "GET", "/jsperf.app/@nosuch/runner.html", "", nil, false)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// The bare project path is the canonical site URL. Naming the default branch
// says nothing extra, so "@<default>" collapses INTO the bare URL -- redirects
// only ever run toward the shorter spelling, never away from it.
func TestBranchSigil_DefaultBranchCollapsesToBareURL(t *testing.T) {
	env := setupEnv(t)
	seedProject(t, env.db, "p")
	env.uploadSite(t, "p", "master", map[string]string{"index.html": "root", "a/x.css": "body{}"})

	for path, want := range map[string]string{
		"/p/@master/":        "/p/",
		"/p/@master":         "/p/",
		"/p/@master/a/x.css": "/p/a/x.css",
	} {
		rec := env.do(t, "GET", path, "", nil, false)
		require.Equalf(t, http.StatusFound, rec.Code, "GET %s: %s", path, rec.Body.String())
		assert.Equalf(t, want, rec.Header().Get("Location"), "GET %s", path)
		// Which branch the bare URL means can change with the next publish.
		assert.Equalf(t, "no-store", rec.Header().Get("Cache-Control"), "GET %s", path)
	}

	// ...and the bare URL it points at serves the file, in one hop, with no
	// redirect of its own back to a branch URL.
	for path, want := range map[string]string{
		"/p/":        "root",
		"/p/a/x.css": "body{}",
	} {
		rec := env.do(t, "GET", path, "", nil, false)
		require.Equalf(t, http.StatusOK, rec.Code, "GET %s: %s", path, rec.Body.String())
		assert.Equalf(t, want, rec.Body.String(), "GET %s", path)
	}

	// The bare root without its slash canonicalizes to the slashed form only --
	// never to a branch URL.
	rec := env.do(t, "GET", "/p", "", nil, false)
	require.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/p/", rec.Header().Get("Location"))
}

// /branch/{branch}/ is what every published preview link, README and deployed
// client already says, so it keeps working -- as a 302 to the canonical URL for
// the same file. It stops being a second place that serves bytes, and every
// client that follows redirects (all of them, for GET) is unaffected.
func TestBranchSigil_LegacyFormRedirects(t *testing.T) {
	env := setupEnv(t)
	seedProject(t, env.db, "p")
	env.uploadSite(t, "p", "master", map[string]string{"index.html": "root", "a/x.css": "body{}"})
	env.uploadSite(t, "p", "pr-1", map[string]string{"index.html": "preview", "a/x.css": "pv{}"})

	for path, want := range map[string]string{
		// The default branch: the shortest URL there is.
		"/p/branch/master/a/x.css": "/p/a/x.css",
		"/p/branch/master/":        "/p/",
		"/p/branch/master":         "/p/",
		// Any other branch: the "@" spelling of the same ref.
		"/p/branch/pr-1/a/x.css": "/p/@pr-1/a/x.css",
		"/p/branch/pr-1/":        "/p/@pr-1/",
		"/p/branch/pr-1":         "/p/@pr-1/",
	} {
		rec := env.do(t, "GET", path, "", nil, false)
		require.Equalf(t, http.StatusFound, rec.Code, "GET %s: %s", path, rec.Body.String())
		assert.Equalf(t, want, rec.Header().Get("Location"), "GET %s", path)
	}

	// The query string survives.
	rec := env.do(t, "GET", "/p/branch/pr-1/a/x.css?v=2", "", nil, false)
	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "/p/@pr-1/a/x.css?v=2", rec.Header().Get("Location"))

	// A branch with no site still 404s rather than redirecting somewhere empty.
	rec = env.do(t, "GET", "/p/branch/nosuch/x.css", "", nil, false)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Every target it names actually serves the bytes, in one further hop.
	for path, want := range map[string]string{
		"/p/a/x.css":       "body{}",
		"/p/@pr-1/a/x.css": "pv{}",
	} {
		rec := env.do(t, "GET", path, "", nil, false)
		require.Equalf(t, http.StatusOK, rec.Code, "GET %s: %s", path, rec.Body.String())
		assert.Equalf(t, want, rec.Body.String(), "GET %s", path)
	}

	// The branches listing is a literal segment and outranks everything.
	rec = env.do(t, "GET", "/p/branches", "", nil, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"branch":"master"`)
}

// The sigil marks exactly where the project name ends, so a namespaced project
// needs no lookup to be addressed -- and a shorter project sharing its prefix
// can never claim the URL.
func TestBranchSigil_NamespacedProject(t *testing.T) {
	env := setupEnv(t)
	seedProject(t, env.db, "org")
	seedProject(t, env.db, "org/repo")
	// Each project gets its own default-branch site, so pr-1 stays a non-default
	// ref in both and serves in place rather than collapsing.
	env.uploadSite(t, "org", "master", map[string]string{"index.html": "org-default"})
	env.uploadSite(t, "org/repo", "master", map[string]string{"index.html": "ns-default"})
	env.uploadSite(t, "org", "pr-1", map[string]string{"index.html": "shorter"})
	env.uploadSite(t, "org/repo", "pr-1", map[string]string{"x.css": "ns{}"})

	rec := env.do(t, "GET", "/org/repo/@pr-1/x.css", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "ns{}", rec.Body.String())

	rec = env.do(t, "GET", "/org/@pr-1/", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "shorter", rec.Body.String())
}

// Branch names may contain "/" (claude/foo). "@" says where the ref starts,
// not where it ends, so the remainder is still resolved by longest match
// against the project's sites -- the same rule the /branch/ form uses.
func TestBranchSigil_SlashNamedBranch(t *testing.T) {
	env := setupEnv(t)
	seedProject(t, env.db, "p")
	env.uploadSite(t, "p", "master", map[string]string{"index.html": "default"})
	env.uploadSite(t, "p", "claude", map[string]string{"b.html": "c-b"})
	env.uploadSite(t, "p", "claude/foo", map[string]string{"index.html": "cf-index", "c.html": "cf-c"})

	for path, want := range map[string]string{
		"/p/@claude/foo/":       "cf-index",
		"/p/@claude/foo/c.html": "cf-c",
		"/p/@claude/b.html":     "c-b", // the shorter branch keeps everything else
	} {
		rec := env.do(t, "GET", path, "", nil, false)
		require.Equalf(t, http.StatusOK, rec.Code, "GET %s: %s", path, rec.Body.String())
		assert.Equalf(t, want, rec.Body.String(), "GET %s", path)
	}
}

// Deploy and remove in the "@" form. These need their own routes (the read
// grammar rides the apex route, which is GET-only), and two patterns each,
// because "@{branch}" is one path segment while a branch name may span several.
func TestBranchSigil_UploadAndDelete(t *testing.T) {
	env := setupEnv(t)
	seedProject(t, env.db, "p")
	env.uploadSite(t, "p", "master", map[string]string{"index.html": "default"})

	rec := env.do(t, "PUT", "/p/@pr-3", "application/gzip",
		makeTarGz(t, map[string]string{"index.html": "three"}), true)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// A slash-named branch: the router splits it across two params, and a write
	// names a branch outright, so the whole thing is the branch.
	rec = env.do(t, "PUT", "/p/@claude/foo", "application/gzip",
		makeTarGz(t, map[string]string{"index.html": "cf"}), true)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// Both are readable in the canonical form...
	for path, want := range map[string]string{
		"/p/@pr-3/":       "three",
		"/p/@claude/foo/": "cf",
	} {
		rec := env.do(t, "GET", path, "", nil, false)
		require.Equalf(t, http.StatusOK, rec.Code, "GET %s: %s", path, rec.Body.String())
		assert.Equalf(t, want, rec.Body.String(), "GET %s", path)
	}

	// ...and the legacy URL for each 302s there, slash-named branch included.
	for path, want := range map[string]string{
		"/p/branch/pr-3/":       "/p/@pr-3/",
		"/p/branch/claude/foo/": "/p/@claude/foo/",
	} {
		rec := env.do(t, "GET", path, "", nil, false)
		require.Equalf(t, http.StatusFound, rec.Code, "GET %s: %s", path, rec.Body.String())
		assert.Equalf(t, want, rec.Header().Get("Location"), "GET %s", path)
	}

	// A write still needs a token, exactly like the /branch/ form.
	rec = env.do(t, "PUT", "/p/@pr-4", "application/gzip",
		makeTarGz(t, map[string]string{"index.html": "four"}), false)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	rec = env.do(t, "DELETE", "/p/@claude/foo", "", nil, true)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	rec = env.do(t, "GET", "/p/@claude/foo/", "", nil, false)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = env.do(t, "DELETE", "/p/@pr-3", "", nil, true)
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestSplitBranchSigil(t *testing.T) {
	cases := []struct {
		remainder, project, ref string
		ok                      bool
	}{
		{"p/@main", "p", "main", true},
		{"p/@main/x.css", "p", "main/x.css", true},
		{"p/@main/a/b/x.css", "p", "main/a/b/x.css", true},
		{"org/repo/@main/x.css", "org/repo", "main/x.css", true},
		{"org/repo/inner/@v1", "org/repo/inner", "v1", true},
		// "@" says where the ref starts; the rest stays for splitSiteBranch.
		{"p/@claude/foo/c.html", "p", "claude/foo/c.html", true},
		// A later "@" is part of the file path, not a second ref.
		{"p/@main/a@b.css", "p", "main/a@b.css", true},
		// No sigil: the apex grammar.
		{"p", "", "", false},
		{"p/runner.html", "", "", false},
		// A sigil that names nothing, or names no project.
		{"p/@", "", "", false},
		{"@main/x", "", "", false},
		{"@", "", "", false},
	}
	for _, tc := range cases {
		project, ref, ok := splitBranchSigil(tc.remainder)
		assert.Equalf(t, tc.ok, ok, "ok for %q", tc.remainder)
		assert.Equalf(t, tc.project, project, "project for %q", tc.remainder)
		assert.Equalf(t, tc.ref, ref, "ref for %q", tc.remainder)
	}
}

// uploadSiteAtCommit deploys a branch's site recording the commit it was built
// from -- the X-Git-Commit header every publish already sends.
func (e *testEnv) uploadSiteAtCommit(t *testing.T, project, branch, commit string, files map[string]string) {
	t.Helper()
	req := httptest.NewRequest("PUT", "/"+project+"/@"+branch, bytes.NewReader(makeTarGz(t, files)))
	req.Host = sitesHost
	req.Header.Set("Content-Type", "application/gzip")
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("X-Git-Commit", commit)
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

// A commit sha names the BUILD rather than the branch, so a link can pin the
// exact site it was tested against instead of tracking whatever the branch
// moves to. Every publish already records the commit (X-Git-Commit, defaulted
// to github.sha by the publish action), so this needs nothing new from callers.
func TestCommitSigil_Serves(t *testing.T) {
	const sha = "0f1e2d3c4b5a69788796a5b4c3d2e1f001234567"
	env := setupEnv(t)
	seedProject(t, env.db, "p")
	env.uploadSiteAtCommit(t, "p", "master", "9999999999999999999999999999999999999999",
		map[string]string{"index.html": "default"})
	env.uploadSiteAtCommit(t, "p", "pr-7", sha,
		map[string]string{"index.html": "preview", "a/x.css": "body{}"})

	// The full sha, and any abbreviation git itself would accept.
	for _, ref := range []string{sha, sha[:12], sha[:7]} {
		rec := env.do(t, "GET", "/p/@"+ref+"/a/x.css", "", nil, false)
		require.Equalf(t, http.StatusOK, rec.Code, "GET @%s: %s", ref, rec.Body.String())
		assert.Equalf(t, "body{}", rec.Body.String(), "GET @%s", ref)

		rec = env.do(t, "GET", "/p/@"+ref+"/", "", nil, false)
		require.Equalf(t, http.StatusOK, rec.Code, "GET @%s/", ref)
		assert.Equalf(t, "preview", rec.Body.String(), "GET @%s/", ref)
	}

	// Case-insensitive, like git.
	rec := env.do(t, "GET", "/p/@"+strings.ToUpper(sha[:10])+"/a/x.css", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "body{}", rec.Body.String())

	// A commit ref is the MOST specific spelling there is, so it is never
	// collapsed into the bare URL even when it resolves the default branch --
	// that would throw the pin away.
	rec = env.do(t, "GET", "/p/@9999999999/", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "default", rec.Body.String())

	// Too short to be a deliberate commit reference (git abbreviates to 7), and
	// an unknown sha: both 404 rather than falling through to a file.
	for _, ref := range []string{"0f1e2d", "abcdef1234567890"} {
		rec := env.do(t, "GET", "/p/@"+ref+"/a/x.css", "", nil, false)
		assert.Equalf(t, http.StatusNotFound, rec.Code, "GET @%s", ref)
	}

	// Once the branch redeploys, the old commit stops resolving -- the URL
	// serves exactly that build or nothing, never a later one under the same sha.
	env.uploadSiteAtCommit(t, "p", "pr-7", "1111111111111111111111111111111111111111",
		map[string]string{"index.html": "rebuilt"})
	rec = env.do(t, "GET", "/p/@"+sha+"/", "", nil, false)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = env.do(t, "GET", "/p/@1111111/", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "rebuilt", rec.Body.String())
}

// A branch is always tried before a commit, so a branch whose name happens to
// be hex keeps its URL -- no ref that resolved before can be repointed.
func TestCommitSigil_BranchWins(t *testing.T) {
	const hexBranch = "abcdef0"
	env := setupEnv(t)
	seedProject(t, env.db, "p")
	env.uploadSiteAtCommit(t, "p", "master", "2222222222222222222222222222222222222222",
		map[string]string{"index.html": "default"})
	env.uploadSiteAtCommit(t, "p", "other", hexBranch+"333333333333333333333333333333333",
		map[string]string{"index.html": "by-commit"})
	env.uploadSiteAtCommit(t, "p", hexBranch, "4444444444444444444444444444444444444444",
		map[string]string{"index.html": "by-branch"})

	rec := env.do(t, "GET", "/p/@"+hexBranch+"/", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "by-branch", rec.Body.String())
}

func TestLooksLikeCommit(t *testing.T) {
	for _, s := range []string{"abcdef0", "0123456789abcdef", strings.Repeat("a", 40), "ABCDEF0"} {
		assert.Truef(t, looksLikeCommit(s), "%q should look like a commit", s)
	}
	for _, s := range []string{"", "abcdef", "main", "pr-7", strings.Repeat("a", 41), "abcdefg", "abcdef0/x"} {
		assert.Falsef(t, looksLikeCommit(s), "%q should not look like a commit", s)
	}
}
