package sites

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// rootServed drives the bare project root ("/{project}/") and returns the body
// it served. Each branch's index.html carries a distinct marker, so the body
// names the branch resolveRootBranch picked -- the same chain the public-read
// gate uses.
func rootServed(t *testing.T, h *Handler, project *db.Project) string {
	t.Helper()
	req := httptest.NewRequest("GET", "http://sites.example.com/"+project.Name+"/", nil)
	req = withRoute(req, project, route{project: project.Name, root: true})
	rec := httptest.NewRecorder()
	h.ServeDefaultBranch(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return rec.Body.String()
}

// A project whose default_branch points at a branch with no published site
// (e.g. the seed "master" while sites were only ever deployed to "main",
// because buildhost's GitHub default-branch lookup hasn't corrected the hint)
// must still resolve its bare root to a real site instead of serving a
// guaranteed 404. This is the ue553 case: default_branch stuck at master,
// site on main.
func TestRootServe_FallsBackToExistingSite(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "ue553")
	uploadSite(t, h, proj, "main", map[string]string{"index.html": "from-main"})

	// default_branch stuck at the seed "master" (no site there) -> fall back to main.
	proj.DefaultBranch = "master"
	assert.Equal(t, "from-main", rootServed(t, h, proj))

	// Unset default_branch behaves the same (defaultBranch() seeds "master").
	proj.DefaultBranch = ""
	assert.Equal(t, "from-main", rootServed(t, h, proj))

	// Once the default branch has its own site, the root uses it unchanged.
	uploadSite(t, h, proj, "master", map[string]string{"index.html": "from-master"})
	proj.DefaultBranch = "master"
	assert.Equal(t, "from-master", rootServed(t, h, proj))
}

// The fallback prefers the conventional "main"/"master" over a more recently
// updated ephemeral PR-preview branch, so the canonical root never lands on a
// transient preview even when the preview was deployed last.
func TestRootServe_PrefersMainOverRecentPreview(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "proj")
	uploadSite(t, h, proj, "main", map[string]string{"index.html": "main"})
	uploadSite(t, h, proj, "pr-9", map[string]string{"index.html": "pr"}) // deployed later

	proj.DefaultBranch = "develop" // no site on develop
	assert.Equal(t, "main", rootServed(t, h, proj))
}

// With no sites at all the root 404s (the default branch has nothing to serve)
// -- the fallback never invents a branch out of nothing.
func TestRootServe_NoSitesNotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "empty")
	proj.DefaultBranch = "main"

	req := httptest.NewRequest("GET", "http://sites.example.com/empty/", nil)
	req = withRoute(req, proj, route{project: "empty", root: true})
	rec := httptest.NewRecorder()
	h.ServeDefaultBranch(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// The project root without its trailing slash canonicalizes to the slashed
// form, so relative links in index.html resolve under the project rather than
// the host root. It must NOT redirect to a branch URL: that is the longer,
// more fragile spelling, and pointing the short URL at it is backwards.
func TestRootServe_TrailingSlashCanonicalization(t *testing.T) {
	h, d, _ := setupTest(t)
	for _, name := range []string{"ue553", "org/repo"} {
		proj := seedProject(t, d, name)
		uploadSite(t, h, proj, "main", map[string]string{"index.html": "hi"})
		proj.DefaultBranch = "main"

		req := httptest.NewRequest("GET", "http://sites.example.com/"+name, nil)
		req = withRoute(req, proj, route{project: name, root: true})
		rec := httptest.NewRecorder()
		h.ServeDefaultBranch(rec, req)

		assert.Equalf(t, http.StatusMovedPermanently, rec.Code, "GET /%s", name)
		assert.Equalf(t, "/"+name+"/", rec.Header().Get("Location"), "GET /%s", name)
	}
}
