package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

// TestRequireProject_ReadAccess_NeverAutoProvisions proves a read request does
// not create a missing project even when an OIDC token would authorize a write
// provision: auto-provisioning is a write-only action, so a GET can never
// materialize a project as a side effect.
func TestRequireProject_ReadAccess_NeverAutoProvisions(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "log-streamer", access: ReadAccess}
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	handler := requireProjectFunc(parse, inner)

	tok := &db.APIToken{ID: -1, Scopes: "read,write"}
	ctx := WithOIDCProject(WithToken(context.Background(), tok), "log-streamer")
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	_, err := d.GetProject(context.Background(), "log-streamer")
	assert.ErrorIs(t, err, db.ErrNotFound, "read must not have created the project")
}

// TestRequireProject_HiddenReadAccess_PrivateProject_Returns404 proves
// HiddenReadAccess hides a private project from an unauthorized caller behind a
// 404 (not the 401 ReadAccess returns), so its existence never leaks.
func TestRequireProject_HiddenReadAccess_PrivateProject_Returns404(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{Name: "secret", IsPrivate: true, Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "secret", access: HiddenReadAccess}
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	handler := requireProjectFunc(parse, inner)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Identical to an unknown project: 404, not 401/403.
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
