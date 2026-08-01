package sites

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The canonical branch spelling is /{project}/@{branch}/{path}. It exists
// because "branch" is an ordinary path segment: a site holding a directory
// called "branch" cannot be addressed at all through the older form, and every
// URL in that form carries a segment that reads like part of the site. "@" is
// outside both the branch and the project-name charsets, so the split is exact.
//
// These go through the REAL router, because that is where the risk is: the "@"
// reads share the literal-less apex route, which must lose to /branch/ and
// /branches while still catching everything else.
func TestBranchSigil_Serves(t *testing.T) {
	env := setupEnv(t)
	seedProject(t, env.db, "jsperf.app")
	env.uploadSite(t, "jsperf.app", "main", map[string]string{
		"index.html":     "<h1>root</h1>",
		"runner.html":    "<h1>runner</h1>",
		"assets/app.js":  "console.log(1)",
		"sub/index.html": "<h1>sub</h1>",
	})
	env.uploadSite(t, "jsperf.app", "pr-7", map[string]string{
		"index.html":  "<h1>preview</h1>",
		"runner.html": "<h1>pr runner</h1>",
	})

	for path, want := range map[string]string{
		"/jsperf.app/@main/runner.html":   "<h1>runner</h1>",
		"/jsperf.app/@main/assets/app.js": "console.log(1)",
		"/jsperf.app/@main/sub/":          "<h1>sub</h1>", // directory -> index.html
		"/jsperf.app/@main/":              "<h1>root</h1>",
		"/jsperf.app/@pr-7/runner.html":   "<h1>pr runner</h1>",
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

	// A branch with no site 404s; it never falls through to a same-named file.
	rec = env.do(t, "GET", "/jsperf.app/@nosuch/runner.html", "", nil, false)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// Neither older spelling may change: /branch/{branch}/ is what every published
// preview link, README and deployed client already says, and the apex path is
// what a bare project URL means. The two forms address the same bytes.
func TestBranchSigil_OlderFormsUnchanged(t *testing.T) {
	env := setupEnv(t)
	seedProject(t, env.db, "p")
	env.uploadSite(t, "p", "main", map[string]string{"index.html": "root", "a/x.css": "body{}"})
	env.uploadSite(t, "p", "pr-1", map[string]string{"index.html": "preview"})

	for path, want := range map[string]string{
		"/p/branch/main/a/x.css": "body{}", // the original form
		"/p/@main/a/x.css":       "body{}", // ...and the canonical one
		"/p/a/x.css":             "body{}", // ...and the apex path
		"/p/branch/pr-1/":        "preview",
		"/p/@pr-1/":              "preview",
	} {
		rec := env.do(t, "GET", path, "", nil, false)
		require.Equalf(t, http.StatusOK, rec.Code, "GET %s: %s", path, rec.Body.String())
		assert.Equalf(t, want, rec.Body.String(), "GET %s", path)
	}

	// The branches listing is a literal segment and outranks everything.
	rec := env.do(t, "GET", "/p/branches", "", nil, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"branch":"main"`)
}

// The sigil marks exactly where the project name ends, so a namespaced project
// needs no lookup to be addressed -- and a shorter project sharing its prefix
// can never claim the URL.
func TestBranchSigil_NamespacedProject(t *testing.T) {
	env := setupEnv(t)
	seedProject(t, env.db, "org")
	seedProject(t, env.db, "org/repo")
	env.uploadSite(t, "org", "main", map[string]string{"index.html": "shorter"})
	env.uploadSite(t, "org/repo", "main", map[string]string{"x.css": "ns{}"})

	rec := env.do(t, "GET", "/org/repo/@main/x.css", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "ns{}", rec.Body.String())

	rec = env.do(t, "GET", "/org/@main/", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "shorter", rec.Body.String())
}

// Branch names may contain "/" (claude/foo). "@" says where the branch starts,
// not where it ends, so the remainder is still resolved by longest match
// against the project's sites -- the same rule the /branch/ form uses.
func TestBranchSigil_SlashNamedBranch(t *testing.T) {
	env := setupEnv(t)
	seedProject(t, env.db, "p")
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

	rec := env.do(t, "PUT", "/p/@pr-3", "application/gzip",
		makeTarGz(t, map[string]string{"index.html": "three"}), true)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// A slash-named branch: the router splits it across two params, and a write
	// names a branch outright, so the whole thing is the branch.
	rec = env.do(t, "PUT", "/p/@claude/foo", "application/gzip",
		makeTarGz(t, map[string]string{"index.html": "cf"}), true)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// Both are readable in every form.
	for path, want := range map[string]string{
		"/p/@pr-3/":             "three",
		"/p/branch/pr-3/":       "three",
		"/p/@claude/foo/":       "cf",
		"/p/branch/claude/foo/": "cf",
	} {
		rec := env.do(t, "GET", path, "", nil, false)
		require.Equalf(t, http.StatusOK, rec.Code, "GET %s: %s", path, rec.Body.String())
		assert.Equalf(t, want, rec.Body.String(), "GET %s", path)
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
		// "@" says where the branch starts; the rest stays for splitSiteBranch.
		{"p/@claude/foo/c.html", "p", "claude/foo/c.html", true},
		// A later "@" is part of the file path, not a second branch.
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
