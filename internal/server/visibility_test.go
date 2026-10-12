package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

// publishRelease creates a project, publishes one release per payload, and
// returns the project.
func publishRelease(t *testing.T, env *testEnv, name string, private bool, payloads ...string) *db.Project {
	t.Helper()
	body := `{"name":"` + name + `","versioning":"auto","is_private":false}`
	if private {
		body = `{"name":"` + name + `","versioning":"auto","is_private":true}`
	}
	require.Equal(t, http.StatusCreated, env.postJSON(t, "/api/v1/projects", body).StatusCode)
	for i, payload := range payloads {
		version := string(rune('1' + i))
		require.Equal(t, http.StatusCreated, env.postJSON(t, "/api/v1/projects/"+name+"/releases", `{"git_branch":"main"}`).StatusCode)
		require.Equal(t, http.StatusCreated, env.putBody(t, "/api/v1/projects/"+name+"/releases/"+version+"/artifacts/linux/amd64", []byte(payload)).StatusCode)
		require.Equal(t, http.StatusOK, env.postJSON(t, "/api/v1/projects/"+name+"/releases/"+version+"/publish", `{}`).StatusCode)
	}
	p, err := env.database.GetProject(context.Background(), name)
	require.NoError(t, err)
	return p
}

// staticPath is the canonical static URL of a release's linux/amd64 raw file.
func staticPath(project, version string) string {
	return "/file?arch=amd64&fmt=raw&os=linux&project=" + project + "&v=" + version
}

// anonStatus fetches a URL with no credential and answers the status.
func anonStatus(t *testing.T, env *testEnv, subdomain, path string) int {
	t.Helper()
	resp := env.getSubdomain(t, subdomain, path)
	resp.Body.Close()
	return resp.StatusCode
}

func setReleaseMode(t *testing.T, env *testEnv, p *db.Project, version string, mode db.Visibility) {
	t.Helper()
	ctx := context.Background()
	rel, err := env.database.GetRelease(ctx, p.ID, version)
	require.NoError(t, err)
	require.NoError(t, env.database.SetReleaseVisibility(ctx, rel.ID, mode))
}

// A build override decides who may download that build, on every download path.
func TestBuildVisibility_DownloadPaths(t *testing.T) {
	t.Serial()
	env := setup(t)

	pub := publishRelease(t, env, "pubapp", false, "build-one", "build-two")
	setReleaseMode(t, env, pub, "2", db.VisibilityPrivate)

	assert.Equal(t, http.StatusOK, anonStatus(t, env, "static", staticPath("pubapp", "1")), "an auto build follows its public project")
	assert.Equal(t, http.StatusUnauthorized, anonStatus(t, env, "static", staticPath("pubapp", "2")), "a forced-private build needs a token")
	assert.Equal(t, http.StatusUnauthorized, anonStatus(t, env, "dl", "/pubapp?arch=amd64&os=linux&v=2"), "dl refuses the forced-private build too")

	resp := env.authGetSubdomain(t, "static", staticPath("pubapp", "2"))
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "a token still reads the forced-private build")
	assert.Contains(t, resp.Header.Get("Cache-Control"), "private", "a private build is never cached as public")

	priv := publishRelease(t, env, "privapp", true, "secret-build")
	assert.Equal(t, http.StatusUnauthorized, anonStatus(t, env, "static", staticPath("privapp", "1")))
	setReleaseMode(t, env, priv, "1", db.VisibilityPublic)
	assert.Equal(t, http.StatusOK, anonStatus(t, env, "static", staticPath("privapp", "1")), "a forced-public build of a private project downloads with no token")
	resp = env.getSubdomain(t, "dl", "/privapp?arch=amd64&os=linux&v=1")
	resp.Body.Close()
	assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode, "dl answers with a plain redirect, not a refusal")
}

// Folder and project overrides change the project's own visibility, and the
// most specific one wins.
func TestFolderAndProjectVisibility_Downloads(t *testing.T) {
	t.Serial()
	env := setup(t)
	ctx := context.Background()

	p := publishRelease(t, env, "org/tool", false, "tool-build")
	assert.Equal(t, http.StatusOK, anonStatus(t, env, "static", staticPath("org/tool", "1")))

	require.NoError(t, env.database.SetVisibilityFolder(ctx, "org", db.VisibilityPrivate))
	assert.Equal(t, http.StatusUnauthorized, anonStatus(t, env, "static", staticPath("org/tool", "1")), "a private folder covers the project")

	require.NoError(t, env.database.SetProjectVisibilityMode(ctx, p, db.VisibilityPublic))
	assert.Equal(t, http.StatusOK, anonStatus(t, env, "static", staticPath("org/tool", "1")), "the project override beats the folder")

	require.NoError(t, env.database.SetProjectVisibilityMode(ctx, p, db.VisibilityAuto))
	require.NoError(t, env.database.SetVisibilityFolder(ctx, "org", db.VisibilityAuto))
	assert.Equal(t, http.StatusOK, anonStatus(t, env, "static", staticPath("org/tool", "1")), "auto everywhere returns to the source value")
}
