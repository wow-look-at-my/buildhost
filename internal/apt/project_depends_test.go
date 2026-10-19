package apt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func getInstallScript(t *testing.T, h *Handler, proj *db.Project) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/"+proj.Name+"/install.sh", nil)
	req.Host = "apt.pazer.build"
	req = withRoute(req, proj, route{project: proj.Name, subPath: "install.sh"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServePackages_CarriesProjectDepends(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()
	proj, _, _ := seedAptProject(t, d, store, "api-cli", "binary-bytes", false)
	require.NoError(t, d.CreateProject(ctx, &db.Project{Name: "ascii-timeline", Versioning: db.VersioningAuto}))
	require.NoError(t, d.CreateProject(ctx, &db.Project{Name: "team/tool_x", Versioning: db.VersioningAuto}))
	require.NoError(t, d.SetProjectAptDepends(ctx, proj.ID, "bubblewrap | docker.io"))
	require.NoError(t, d.SetProjectDepends(ctx, proj.ID, []string{"team/tool_x", "ascii-timeline"}))
	proj, err := d.GetProject(ctx, "api-cli")
	require.NoError(t, err)

	rec := getPackages(t, h, "api-cli", proj)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "\nDepends: ascii-timeline, team-tool-x, bubblewrap | docker.io\n",
		"each dependency appears as its deb package name, ahead of apt_depends")
}

func TestServeInstallScript_AddsDependencyRepositories(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()
	proj := &db.Project{Name: "api-cli", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, proj))
	dep := &db.Project{Name: "ascii-timeline", Versioning: db.VersioningAuto, IsPrivate: true}
	require.NoError(t, d.CreateProject(ctx, dep))
	nested := &db.Project{Name: "team/lib", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, nested))
	require.NoError(t, d.SetProjectDepends(ctx, proj.ID, []string{"ascii-timeline"}))
	require.NoError(t, d.SetProjectDepends(ctx, dep.ID, []string{"team/lib"}))
	proj, err := d.GetProject(ctx, "api-cli")
	require.NoError(t, err)

	rec := getInstallScript(t, h, proj)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "add_dep 'ascii-timeline' 'https://apt.pazer.build/ascii-timeline' 'buildhost-ascii-timeline' 1\n",
		"a private dependency demands the token")
	assert.Contains(t, body, "add_dep 'team/lib' 'https://apt.pazer.build/team/lib' 'buildhost-team-lib' 0\n",
		"a dependency of a dependency also needs its repository")
	assert.Contains(t, body, `echo "deb [signed-by=/etc/apt/keyrings/$dep_slug.asc] $dep_url stable main" > "/etc/apt/sources.list.d/$dep_slug.list"`)
	assert.Less(t, strings.Index(body, `if [ -n "$TOKEN" ]; then`), strings.Index(body, "add_dep 'ascii-timeline'"),
		"the per-project repositories are the tokenless path; a token takes the one authenticated source")
	assert.Less(t, strings.Index(body, "add_dep 'team/lib'"), strings.Index(body, "\napt-get update\n"),
		"every repository is in place before the index refresh")
	assert.Contains(t, body, "sudo apt-get install $PKG")
}

func TestServeInstallScript_NoDependsNoRepositories(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := &db.Project{Name: "myapp", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	rec := getInstallScript(t, h, proj)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "add_dep '")
}

func TestServeInstallScript_MissingDependencyFailsLoud(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := &db.Project{Name: "api-cli", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(context.Background(), proj))
	proj.Depends = "gone"

	rec := getInstallScript(t, h, proj)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), `depends on "gone", which does not exist`)
}
