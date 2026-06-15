package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

// Programmatic clients (no text/html in Accept) get the bare JSON 401 with no
// redirect -- unchanged contract.
func TestRequireProject_PrivateProject_Programmatic_PlainJSON401(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{Name: "secret", IsPrivate: true, Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))
	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "secret", access: ReadAccess}
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	handler := requireProjectFunc(parse, inner)

	req := httptest.NewRequest("GET", "/api/v1/projects/secret", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, rec.Header().Get("Location"))
	assert.Contains(t, rec.Body.String(), "authentication required")
}

// When Sign in with GitHub is NOT configured, even a browser request falls back
// to the plain JSON 401 (no redirect) -- buildhost has nowhere to send them.
func TestRequireProject_PrivateProject_Browser_NoGitHubAuth_PlainJSON401(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d) // mw.GitHub is nil -> disabled

	proj := &db.Project{Name: "secret", IsPrivate: true, Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))
	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "secret", access: ReadAccess}
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	handler := requireProjectFunc(parse, inner)

	req := httptest.NewRequest("GET", "/secret/branch/pr-190/", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, rec.Header().Get("Location"))
}
