package sites

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// rootRedirectTarget drives the bare-root handler and returns its Location.
func rootRedirectTarget(t *testing.T, h *Handler, project *db.Project) string {
	t.Helper()
	req := httptest.NewRequest("GET", "http://sites.example.com/"+project.Name, nil)
	req = withRoute(req, project, route{project: project.Name, root: true})
	rec := httptest.NewRecorder()
	h.RedirectToDefaultBranch(rec, req)
	require.Equal(t, http.StatusFound, rec.Code)
	return rec.Header().Get("Location")
}

// A project whose default_branch points at a branch with no published site
// (e.g. the seed "master" while sites were only ever deployed to "main",
// because buildhost's GitHub default-branch lookup hasn't corrected the hint)
// must still resolve its bare root to a real site instead of bouncing the user
// to a guaranteed 404. This is the ue553 case: default_branch stuck at master,
// site on main.
func TestRootRedirect_FallsBackToExistingSite(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "ue553")
	uploadSite(t, h, proj, "main", map[string]string{"index.html": "<h1>hi</h1>"})

	// default_branch stuck at the seed "master" (no site there) -> fall back to main.
	proj.DefaultBranch = "master"
	assert.Equal(t, "/ue553/branch/main/", rootRedirectTarget(t, h, proj))

	// Unset default_branch behaves the same (defaultBranch() seeds "master").
	proj.DefaultBranch = ""
	assert.Equal(t, "/ue553/branch/main/", rootRedirectTarget(t, h, proj))

	// Once the default branch has its own site, the redirect uses it unchanged.
	uploadSite(t, h, proj, "master", map[string]string{"index.html": "<h1>m</h1>"})
	proj.DefaultBranch = "master"
	assert.Equal(t, "/ue553/branch/master/", rootRedirectTarget(t, h, proj))
}

// The fallback prefers the conventional "main"/"master" over a more recently
// updated ephemeral PR-preview branch, so the canonical root never lands on a
// transient preview even when the preview was deployed last.
func TestRootRedirect_PrefersMainOverRecentPreview(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "proj")
	uploadSite(t, h, proj, "main", map[string]string{"index.html": "main"})
	uploadSite(t, h, proj, "pr-9", map[string]string{"index.html": "pr"}) // deployed later

	proj.DefaultBranch = "develop" // no site on develop
	assert.Equal(t, "/proj/branch/main/", rootRedirectTarget(t, h, proj))
}

// With no sites at all, the redirect keeps targeting the default branch (Serve
// then 404s) -- the fallback never invents a branch out of nothing.
func TestRootRedirect_NoSitesKeepsDefault(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "empty")
	proj.DefaultBranch = "main"
	assert.Equal(t, "/empty/branch/main/", rootRedirectTarget(t, h, proj))
}
