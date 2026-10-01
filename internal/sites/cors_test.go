package sites

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A browser re-checks CORS on EVERY hop of a cross-origin fetch, so a redirect
// that omits the site headers fails the whole load even though its target
func TestSiteRedirectsCarryCORSHeaders(t *testing.T) {
	t.Serial()
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "lib")
	uploadSite(t, h, proj, "main", map[string]string{"index.html": "root", "ui/x.js": "export {}"})
	uploadSite(t, h, proj, "pr-7", map[string]string{"index.html": "preview"})
	proj.DefaultBranch = "main"

	cases := []struct {
		name    string
		reqPath string
		rt      route
		serve   func(http.ResponseWriter, *http.Request)
	}{
		{"legacy branch file", "/lib/branch/main/ui/x.js",
			route{project: "lib", branch: "main", path: "ui/x.js"}, h.RedirectLegacyBranch},
		{"legacy branch root", "/lib/branch/main/",
			route{project: "lib", branch: "main"}, h.RedirectLegacyBranch},
		{"legacy non-default branch", "/lib/branch/pr-7/index.html",
			route{project: "lib", branch: "pr-7", path: "index.html"}, h.RedirectLegacyBranch},

		// @<default-branch> collapsing into the bare project path.
		{"sigil default collapse", "/lib/@main/ui/x.js",
			route{project: "lib", sigil: "main/ui/x.js"}, h.Serve},

		// A branch root missing its trailing slash.
		{"sigil branch root slash", "/lib/@pr-7",
			route{project: "lib", sigil: "pr-7"}, h.Serve},

		// The apex project root missing its trailing slash.
		{"apex root slash", "/lib",
			route{project: "lib", root: true}, h.ServeDefaultBranch},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://sites.example.com"+tc.reqPath, nil)
			req.Header.Set("Origin", "https://consumer.example.com")
			req = withRoute(req, proj, tc.rt)
			rec := httptest.NewRecorder()
			tc.serve(rec, req)

			require.Truef(t, rec.Code == http.StatusFound || rec.Code == http.StatusMovedPermanently,
				"expected a redirect, got %d: %s", rec.Code, rec.Body.String())
			assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"),
				"redirect to %q drops CORS; a cross-origin fetch fails at this hop",
				rec.Header().Get("Location"))
		})
	}
}

// The {project}.<site-domain> scheme emits the same canonicalizing redirects
// and needs the same headers on each of them.
func TestSubdomainRedirectsCarryCORSHeaders(t *testing.T) {
	t.Serial()
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "lib")
	uploadSite(t, h, proj, "main", map[string]string{"index.html": "root", "ui/x.js": "export {}"})
	uploadSite(t, h, proj, "pr-7", map[string]string{"index.html": "preview"})
	proj.DefaultBranch = "main"

	cases := []struct {
		name    string
		reqPath string
		rt      route
	}{
		{"legacy sigil", "/~main/ui/x.js", route{project: "lib", sigil: "main/ui/x.js"}},
		{"sigil default collapse", "/@main/ui/x.js", route{project: "lib", sigil: "main/ui/x.js"}},
		{"branch root slash", "/@pr-7", route{project: "lib", sigil: "pr-7"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://lib.sites.example.com"+tc.reqPath, nil)
			req.Header.Set("Origin", "https://consumer.example.com")
			req = withRoute(req, proj, tc.rt)
			rec := httptest.NewRecorder()
			h.ServeSubdomain(rec, req)

			require.Truef(t, rec.Code == http.StatusFound || rec.Code == http.StatusMovedPermanently,
				"expected a redirect, got %d: %s", rec.Code, rec.Body.String())
			assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"),
				"redirect to %q drops CORS", rec.Header().Get("Location"))
		})
	}
}
