package sites

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/router"
)

// TestRootRedirectRouteShadowing proves the bare GET /{project} root route
// matches /{project} and /{project}/ (binding the project verbatim, no trailing
// slash) yet never shadows the more specific branch / branches routes. The
// router is best-match: more literal segments win, so the literal-less root
// route only catches paths that aren't one of the others. This is the exact
// shadowing failure documented in serve.go (a higher-scoring route eating the
// {path...} route), guarded here against regression on the real router.
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

// TestRedirectToDefaultBranch checks the bare-root handler emits a no-store 302
// to the project's default branch, preserving the trailing slash and a
// namespaced project name, and falling back to the seed default when unset.
func TestRedirectToDefaultBranch(t *testing.T) {
	h := &Handler{}
	cases := []struct {
		name          string
		projectName   string
		defaultBranch string
		reqPath       string
		wantLoc       string
	}{
		// Learned default branch (e.g. ue553 releases off "main").
		{"learned_main", "ue553", "main", "/ue553", "/ue553/branch/main/"},
		{"learned_main_slash", "ue553", "main", "/ue553/", "/ue553/branch/main/"},
		// Unset default branch falls back to the schema/seed default ("master").
		{"seed_master", "foo", "", "/foo", "/foo/branch/master/"},
		// Namespaced project name is preserved verbatim in the target.
		{"namespaced", "org/repo", "v1", "/org/repo", "/org/repo/branch/v1/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := &db.Project{Name: tc.projectName, DefaultBranch: tc.defaultBranch}
			req := httptest.NewRequest("GET", "http://sites.example.com"+tc.reqPath, nil)
			req = withRoute(req, project, route{project: tc.projectName, root: true})
			rec := httptest.NewRecorder()

			h.RedirectToDefaultBranch(rec, req)

			assert.Equal(t, http.StatusFound, rec.Code)
			assert.Equal(t, tc.wantLoc, rec.Header().Get("Location"))
			// The default branch is a mutable pointer -- never cache the redirect.
			assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		})
	}
}
