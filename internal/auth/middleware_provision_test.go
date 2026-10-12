package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

// These cover the docker push-to-create handshake. A buildkit/docker pusher

// Anonymous (no token) write to a non-existent project on a /v2/ path must get
func TestRequireProject_AutoCreate_AnonymousOCIWrite_Returns401Challenge(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	initTestMiddleware(t, d)

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "ue553/benchmark-base", access: WriteAccess}
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for an unauthenticated write")
	})
	handler := requireProjectFunc(parse, inner)

	// No token in context: the scheme-discovery request a docker pusher makes.
	req := httptest.NewRequest("POST", "/v2/ue553/benchmark-base/blobs/uploads/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code, "anonymous push-to-create must be challenged, not 404'd")
	assert.Equal(t, `Basic realm="buildhost"`, rec.Header().Get("Www-Authenticate"))
}

// The same anonymous write on a non-/v2/ path (e.g. the REST publish API) gets a
func TestRequireProject_AutoCreate_AnonymousNonOCIWrite_Returns401(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	initTestMiddleware(t, d)

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "brand-new", access: WriteAccess}
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for an unauthenticated write")
	})
	handler := requireProjectFunc(parse, inner)

	req := httptest.NewRequest("PUT", "/api/v1/projects/brand-new/releases/v1/artifacts/linux/amd64", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, rec.Header().Get("Www-Authenticate"))
}

// A write that presented a JWT which was REJECTED (OIDC error in context, no
func TestRequireProject_AutoCreate_RejectedJWT_Returns401WithReason(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	initTestMiddleware(t, d)

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "ue553/benchmark-base", access: WriteAccess}
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called when the JWT was rejected")
	})
	handler := requireProjectFunc(parse, inner)

	ctx := WithOIDCError(context.Background(), errors.New(`event "scheduled" not in allowed list`))
	req := httptest.NewRequest("POST", "/v2/ue553/benchmark-base/blobs/uploads/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, `Basic realm="buildhost"`, rec.Header().Get("Www-Authenticate"))
	assert.Contains(t, rec.Body.String(), "OIDC token rejected")
}

// With a valid OIDC token authorized for the namespace, the authenticated retry
// (which the client makes after the challenge) provisions the project -- so
func TestRequireProject_AutoCreate_AuthenticatedOCIWrite_Provisions(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	initTestMiddleware(t, d)

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "ue553/benchmark-base", access: WriteAccess}
	}
	var gotProject *db.Project
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProject = ProjectFrom(r.Context())
		w.WriteHeader(http.StatusAccepted)
	})
	handler := requireProjectFunc(parse, inner)

	tok := &db.APIToken{ID: -1, Scopes: "read,write"}
	ctx := WithToken(context.Background(), tok)
	ctx = WithOIDCProject(ctx, "ue553") // the repo owns ue553 and ue553/<...>
	ctx = WithOIDCPrivate(ctx, false)
	req := httptest.NewRequest("POST", "/v2/ue553/benchmark-base/blobs/uploads/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
	require.NotNil(t, gotProject)
	assert.Equal(t, "ue553/benchmark-base", gotProject.Name)

	// Provisioned for real, retrievable by name.
	created, err := d.GetProject(context.Background(), "ue553/benchmark-base")
	require.NoError(t, err)
	assert.Equal(t, "ue553/benchmark-base", created.Name)
}
