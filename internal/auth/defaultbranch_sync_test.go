package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// A GitHub OIDC write resolves the repo's default branch from GitHub and records
// it on the project -- buildhost learns "v1" from the repo identity in the token,
// with nothing sent in the request. This is the go-toolchain fix.
func TestRequireProject_OIDCSyncsDefaultBranch(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	withStubGitHub(t, "", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/repos/wow-look-at-my/go-toolchain", r.URL.Path)
		fmt.Fprint(w, `{"default_branch":"v1"}`)
	})

	proj := &db.Project{Name: "go-toolchain", Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))
	loaded, err := d.GetProject(context.Background(), "go-toolchain")
	require.NoError(t, err)
	require.Equal(t, "master", loaded.DefaultBranch, "new project starts at the master default")

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "go-toolchain", access: WriteAccess}
	}
	var gotProject *db.Project
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProject = ProjectFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := requireProjectFunc(parse, inner)

	tok := &db.APIToken{ID: -1, Scopes: "read,write"}
	ctx := WithToken(context.Background(), tok)
	ctx = WithOIDCProject(ctx, "go-toolchain")
	ctx = WithOIDCRepo(ctx, "wow-look-at-my/go-toolchain", GitHubActionsIssuer)
	req := httptest.NewRequest("PUT", "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, gotProject)
	assert.Equal(t, "v1", gotProject.DefaultBranch, "default branch synced from GitHub")

	reloaded, err := d.GetProject(context.Background(), "go-toolchain")
	require.NoError(t, err)
	assert.Equal(t, "v1", reloaded.DefaultBranch, "synced default branch persisted in DB")
}

// A non-GitHub OIDC issuer must not trigger a github.com lookup -- the registry
// stays provider-agnostic.
func TestRequireProject_NonGitHubIssuer_NoDefaultBranchSync(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	var hits atomic.Int32
	withStubGitHub(t, "", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `{"default_branch":"v1"}`)
	})

	proj := &db.Project{Name: "gitlab-proj", Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "gitlab-proj", access: WriteAccess}
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := requireProjectFunc(parse, inner)

	tok := &db.APIToken{ID: -1, Scopes: "read,write"}
	ctx := WithToken(context.Background(), tok)
	ctx = WithOIDCProject(ctx, "gitlab-proj")
	ctx = WithOIDCRepo(ctx, "group/proj", "https://gitlab.example.com")
	req := httptest.NewRequest("PUT", "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, int32(0), hits.Load(), "non-GitHub issuer must not reach github.com")

	reloaded, err := d.GetProject(context.Background(), "gitlab-proj")
	require.NoError(t, err)
	assert.Equal(t, "master", reloaded.DefaultBranch, "default branch unchanged for non-GitHub issuer")
}
