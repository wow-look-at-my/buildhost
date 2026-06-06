package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestRequireProject_WriteMissingProject_RejectedOIDC_ExplainsReason proves a
// write to a not-yet-existing project whose JWT was rejected (e.g. org not in
// the allowlist) returns a 401 that names the reason -- not a bare "project not
// found" 404. The 404 masked an OIDC org-allowlist rejection as a missing
// project, which is what made the PazerOP/scratch preview failure so opaque. The
// project must not be created.
func TestRequireProject_WriteMissingProject_RejectedOIDC_ExplainsReason(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "scratch", access: WriteAccess}
	}
	handler := requireProjectFunc(parse, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run when the JWT was rejected")
	})

	// Authenticate records why a presented JWT was rejected; no token is set.
	ctx := WithOIDCError(context.Background(), errors.New(`org "PazerOP" not in allowed list`))
	req := httptest.NewRequest("PUT", "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	var resp struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Contains(t, resp.Error, `org "PazerOP" not in allowed list`,
		"401 body should name the OIDC rejection reason")
	_, err := d.GetProject(context.Background(), "scratch")
	assert.ErrorIs(t, err, db.ErrNotFound, "a rejected write must not create the project")
}

// publicReadRouteInfo is a RouteInfo that also opts a read into the public path.
type publicReadRouteInfo struct {
	project		string
	access		AccessLevel
	allowsRead	bool
}

func (r publicReadRouteInfo) ProjectName() string	{ return r.project }
func (r publicReadRouteInfo) Access() AccessLevel	{ return r.access }
func (r publicReadRouteInfo) AllowsPublicRead(context.Context, *db.DB, *db.Project) bool {
	return r.allowsRead
}

// TestRequireProject_PublicRead_ServesPrivateProjectWithoutToken proves a route
// that implements PublicReadAuthorizer (e.g. a static site published public) is
// served without a token even under a private project, while a non-public read
// of the same project still requires auth.
func TestRequireProject_PublicRead_ServesPrivateProjectWithoutToken(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	require.NoError(t, d.CreateProject(context.Background(),
		&db.Project{Name: "priv", IsPrivate: true, Versioning: "auto"}))

	serve := func(allows bool) int {
		parse := func(r *http.Request) RouteInfo {
			return publicReadRouteInfo{project: "priv", access: ReadAccess, allowsRead: allows}
		}
		handler := requireProjectFunc(parse, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		return rec.Code
	}

	assert.Equal(t, http.StatusOK, serve(true), "a public resource serves without a token under a private project")
	assert.Equal(t, http.StatusUnauthorized, serve(false), "a non-public read still requires auth")
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
