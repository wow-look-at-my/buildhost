package sites

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/router"
)

// TestRootRedirectRouteShadowing proves the apex GET /{project} route catches
// the project root and every file path under it, yet never shadows the more
// specific branch / branches routes. The router is best-match: more literal
// segments win, so the literal-less apex route only catches paths that aren't
// one of the others. This is the exact shadowing failure documented in serve.go
// (a higher-scoring route eating the {path...} route), guarded here against
// regression on the real router.
//
// It also pins WHY parseRootRoute re-splits against the DB: {project} has no
// wildcard after it, so it binds the whole remainder greedily -- the router
// hands over "ue553/runner.html" as one string and cannot know where the
// project name ends and the file path begins.
func TestRootRedirectRouteShadowing(t *testing.T) {
	var hit, gotProject string
	mk := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			hit = name
			gotProject = r.PathValue("project")
		}
	}

	r := router.New()
	// Same patterns (and host) registered by handler.go's init().
	r.HandleFunc("GET sites.{domain}/{project}/branch/{branch}/{path...}", router.Allow, mk("serve"))
	r.HandleFunc("GET sites.{domain}/{project}/branches", router.Allow, mk("list"))
	r.HandleFunc("GET sites.{domain}/{project}", router.Allow, mk("root"))

	cases := []struct {
		path        string
		wantHit     string
		wantProject string
	}{
		{"/ue553", "root", "ue553"},
		{"/ue553/", "root", "ue553"},
		{"/org/repo", "root", "org/repo"}, // namespaced project root
		// A file under the project root: the whole remainder arrives as
		// {project}, for parseRootRoute to split.
		{"/ue553/runner.html", "root", "ue553/runner.html"},
		{"/ue553/assets/app.js", "root", "ue553/assets/app.js"},
		{"/ue553/branches", "list", "ue553"},
		{"/ue553/branch/main", "serve", "ue553"},
		{"/ue553/branch/main/", "serve", "ue553"},
		{"/ue553/branch/main/assets/app.js", "serve", "ue553"},
	}
	for _, tc := range cases {
		hit, gotProject = "", ""
		req := httptest.NewRequest("GET", "http://sites.example.com"+tc.path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equalf(t, tc.wantHit, hit, "path %q routed to wrong handler", tc.path)
		assert.Equalf(t, tc.wantProject, gotProject, "path %q bound wrong project", tc.path)
	}
}

// The default branch never needs naming: the bare project path already serves
// it and is shorter, so an "@<default>" URL collapses INTO it. Redirects only
// ever run toward the simpler URL -- the bare root pointing at a branch URL,
// as it once did, was backwards.
func TestSigilDefaultBranchCollapsesToBareURL(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "ue553")
	uploadSite(t, h, proj, "main", map[string]string{"index.html": "root", "a/x.css": "body{}"})
	uploadSite(t, h, proj, "pr-7", map[string]string{"index.html": "preview"})
	proj.DefaultBranch = "main"

	cases := []struct {
		name, reqPath, ref, filePath, wantLoc string
	}{
		{"branch root", "/ue553/@main/", "main", "", "/ue553/"},
		{"file", "/ue553/@main/a/x.css", "main/a/x.css", "", "/ue553/a/x.css"},
		{"dir", "/ue553/@main/a/", "main/a", "", "/ue553/a/"},
		{"no trailing slash", "/ue553/@main", "main", "", "/ue553/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://sites.example.com"+tc.reqPath, nil)
			req = withRoute(req, proj, route{project: "ue553", sigil: tc.ref})
			rec := httptest.NewRecorder()
			h.Serve(rec, req)

			assert.Equal(t, http.StatusFound, rec.Code, rec.Body.String())
			assert.Equal(t, tc.wantLoc, rec.Header().Get("Location"))
			// Which branch the bare URL means is a mutable pointer: never cache.
			assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		})
	}

	// A NON-default branch has no shorter spelling, so it serves in place.
	req := httptest.NewRequest("GET", "http://sites.example.com/ue553/@pr-7/", nil)
	req = withRoute(req, proj, route{project: "ue553", sigil: "pr-7"})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "preview", rec.Body.String())

	// Neither does the legacy /branch/ spelling redirect: it is the
	// compatibility alias every published link already uses, so it serves the
	// same bytes in place rather than bouncing deployed clients.
	req = httptest.NewRequest("GET", "http://sites.example.com/ue553/branch/main/a/x.css", nil)
	req = withRoute(req, proj, route{project: "ue553", branch: "main", path: "a/x.css"})
	rec = httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "body{}", rec.Body.String())
}

// The collapse is skipped when the bare URL would address a DIFFERENT project:
// the apex path splits project from file by longest match, so with projects
// "org" and "org/repo", /org/repo/x.css is org/repo's file. Redirecting org's
// own repo/x.css there would silently point at another project's site.
func TestSigilDefaultBranchKeepsShadowedFileInPlace(t *testing.T) {
	h, d, _ := setupTest(t)
	org := seedProject(t, d, "org")
	seedProject(t, d, "org/repo")
	uploadSite(t, h, org, "main", map[string]string{"repo/x.css": "shadowed{}", "ok.css": "fine{}"})
	org.DefaultBranch = "main"

	// The shadowed path has no usable shorter URL -> served in place, no redirect.
	req := httptest.NewRequest("GET", "http://sites.example.com/org/@main/repo/x.css", nil)
	req = withRoute(req, org, route{project: "org", sigil: "main/repo/x.css"})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "shadowed{}", rec.Body.String())

	// An unshadowed path in the same site still collapses.
	req = httptest.NewRequest("GET", "http://sites.example.com/org/@main/ok.css", nil)
	req = withRoute(req, org, route{project: "org", sigil: "main/ok.css"})
	rec = httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "/org/ok.css", rec.Header().Get("Location"))
}
