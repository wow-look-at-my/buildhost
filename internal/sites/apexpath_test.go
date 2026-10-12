package sites

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A site file must be reachable at the project's own root path
// (sites.{domain}/{project}/{file}), not only under /branch/{branch}/. Anything
// linking to a site -- an MCP App declaring a runner origin, a page's own
// relative asset, a README -- otherwise has to name a branch it has no business
// knowing. These go through the REAL router: the apex route is literal-less and
// must lose to the branch routes while still catching everything else.
func TestApexPath_ServesFilesFromDefaultBranch(t *testing.T) {
	t.Serial()
	env := setupEnv(t)
	seedProject(t, env.db, "jsperf.app")
	env.uploadSite(t, "jsperf.app", "main", map[string]string{
		"index.html":     "<h1>root</h1>",
		"runner.html":    "<h1>runner</h1>",
		"assets/app.js":  "console.log(1)",
		"sub/index.html": "<h1>sub</h1>",
	})

	// The file the branch URL serves is the file the apex URL serves.
	for path, want := range map[string]string{
		"/jsperf.app/runner.html":   "<h1>runner</h1>",
		"/jsperf.app/assets/app.js": "console.log(1)",
		"/jsperf.app/sub/":          "<h1>sub</h1>", // directory -> index.html
	} {
		rec := env.do(t, "GET", path, "", nil, false)
		require.Equalf(t, http.StatusOK, rec.Code, "GET %s: %s", path, rec.Body.String())
		assert.Equalf(t, want, rec.Body.String(), "GET %s", path)
	}

	rec := env.do(t, "GET", "/jsperf.app/", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "<h1>root</h1>", rec.Body.String())

	rec = env.do(t, "GET", "/jsperf.app", "", nil, false)
	require.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/jsperf.app/", rec.Header().Get("Location"))

	// ...and the branch routes still win the routing: the apex route never
	rec = env.do(t, "GET", "/jsperf.app/branch/main/assets/app.js", "", nil, false)
	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "/jsperf.app/assets/app.js", rec.Header().Get("Location"))

	rec = env.do(t, "GET", "/jsperf.app/branches", "", nil, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"branch":"main"`)
}

// A slash-namespaced project's root must still be its root: /org/repo is
// org/repo's bare root, never "the file repo under project org". Longest match
// decides, so adding file serving to the apex path cannot repoint a URL that
// already resolved -- even when the shorter project genuinely holds a file at
// that path.
func TestApexPath_NamespacedProjectRootUnchanged(t *testing.T) {
	t.Serial()
	env := setupEnv(t)
	seedProject(t, env.db, "org")
	seedProject(t, env.db, "org/repo")
	// The decoy: project "org" serves a file at exactly the path the namespaced
	env.uploadSite(t, "org", "main", map[string]string{"repo": "decoy", "ok.txt": "shorter"})
	env.uploadSite(t, "org/repo", "main", map[string]string{"index.html": "<h1>ns</h1>", "x.css": "body{}"})

	// /org/repo is the namespaced project's root -> its own trailing-slash
	rec := env.do(t, "GET", "/org/repo", "", nil, false)
	require.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/org/repo/", rec.Header().Get("Location"))

	rec = env.do(t, "GET", "/org/repo/", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "<h1>ns</h1>", rec.Body.String())

	// Files under each project resolve to that project's own site.
	rec = env.do(t, "GET", "/org/repo/x.css", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "body{}", rec.Body.String())

	rec = env.do(t, "GET", "/org/ok.txt", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "shorter", rec.Body.String())
}

func TestApexPath_UnknownProjectNotFound(t *testing.T) {
	t.Serial()
	env := setupEnv(t)
	seedProject(t, env.db, "known")
	env.uploadSite(t, "known", "main", map[string]string{"index.html": "hi"})

	for _, path := range []string{"/nope", "/nope/x.html", "/known.evil/x.html"} {
		rec := env.do(t, "GET", path, "", nil, false)
		assert.Equalf(t, http.StatusNotFound, rec.Code, "GET %s", path)
	}

	// A real project with no site 404s on a file, like its root redirect's target.
	seedProject(t, env.db, "siteless")
	rec := env.do(t, "GET", "/siteless/x.html", "", nil, false)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// The apex file path resolves its branch through resolveRootBranch, the same
// chain the root redirect and the public-read gate use, so it lands on a branch
// that actually has a site even when projects.default_branch lags behind.
func TestApexPath_UsesResolvedDefaultBranch(t *testing.T) {
	t.Serial()
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "ue553")
	uploadSite(t, h, proj, "main", map[string]string{"runner.html": "<h1>runner</h1>"})
	proj.DefaultBranch = "master" // seed default, no site there

	req := httptest.NewRequest("GET", "http://sites.example.com/ue553/runner.html", nil)
	req = withRoute(req, proj, route{project: "ue553", path: "runner.html", root: true})
	rec := httptest.NewRecorder()
	h.ServeDefaultBranch(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "<h1>runner</h1>", rec.Body.String())
	// Hosted content, so the app's blocking CSP is dropped here as on every
	assert.Empty(t, rec.Header().Get("Content-Security-Policy"))
}

func TestSplitProjectPath(t *testing.T) {
	t.Serial()
	h, d, _ := setupTest(t)
	seedProject(t, d, "app")
	seedProject(t, d, "org")
	seedProject(t, d, "org/repo")
	seedProject(t, d, "org/repo/inner")

	cases := []struct{ remainder, project, path string }{
		{"app", "app", ""},
		{"app/runner.html", "app", "runner.html"},
		{"app/assets/deep/x.js", "app", "assets/deep/x.js"},
		{"org", "org", ""},
		{"org/repo", "org/repo", ""},             // longest match: not org + "repo"
		{"org/repo/inner", "org/repo/inner", ""}, // ...at any depth
		{"org/repo/inner/x", "org/repo/inner", "x"},
		{"org/repo/x.css", "org/repo", "x.css"},
		{"org/other/x.css", "org", "other/x.css"}, // no org/other project
		// Nothing matches: the whole remainder stays the project name, so
		{"nope/x.html", "nope/x.html", ""},
		{"nope", "nope", ""},
	}
	for _, tc := range cases {
		project, path := h.splitProjectPath(context.Background(), tc.remainder)
		assert.Equalf(t, tc.project, project, "project for %q", tc.remainder)
		assert.Equalf(t, tc.path, path, "path for %q", tc.remainder)
	}
}
