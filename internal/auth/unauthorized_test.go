package auth

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

// A browser navigating to a private resource with NO token is redirected to
// buildhost's own sign-in page (carrying a next= back to the resource), rather
// than getting a raw JSON 401 with no way to authenticate.
func TestRequireProject_PrivateProject_Browser_NoToken_RedirectsToLogin(t *testing.T) {
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

	req := httptest.NewRequest("GET", "/secret/branch/pr-190/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	assert.True(t, strings.HasPrefix(loc, loginPath+"?next="), "should redirect to login, got %q", loc)
	assert.Contains(t, loc, "secret") // next= carries the original target
}

// A browser that IS signed in (carries a token) but whose token is insufficient
// must get an access-denied page, NOT another redirect to login -- which would
// loop forever.
func TestRequireProject_PrivateProject_Browser_InsufficientToken_NoRedirectLoop(t *testing.T) {
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

	// A token present in context but without "read" scope.
	req := httptest.NewRequest("GET", "/secret/branch/pr-190/", nil)
	req.Header.Set("Accept", "text/html")
	req = req.WithContext(WithToken(req.Context(), &db.APIToken{ID: 99, Scopes: "write"}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, rec.Header().Get("Location"), "must not redirect (would loop)")
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, rec.Body.String(), "Access denied")
}

// Programmatic clients (no text/html in Accept) keep the bare JSON 401 with no
// redirect and no challenge -- unchanged contract.
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
	assert.Empty(t, rec.Header().Get("Www-Authenticate"))
	assert.Contains(t, rec.Body.String(), "authentication required")
}
