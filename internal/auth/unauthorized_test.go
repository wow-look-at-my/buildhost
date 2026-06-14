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

// A browser navigating to a private resource (Accept: text/html) must get a
// Basic challenge so it shows its native sign-in dialog, plus a readable HTML
// page instead of raw JSON. Without the challenge there is no way to
// authenticate from a browser -- e.g. viewing a private static-site PR preview.
func TestRequireProject_PrivateProject_Browser_Returns401WithChallengeAndHTML(t *testing.T) {
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
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, `Basic realm="buildhost"`, rec.Header().Get("Www-Authenticate"))
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	body := rec.Body.String()
	assert.Contains(t, body, "Authentication required")
	assert.Contains(t, body, "password")
}
