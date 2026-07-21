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

func TestRequireProject_WriteAccess_NoToken_Returns401(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{Name: "pub", Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "pub", access: WriteAccess}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	handler := requireProjectFunc(parse, inner)

	req := httptest.NewRequest("POST", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireProject_WriteAccess_ReadOnlyToken_Returns401(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{Name: "pub", Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "pub", access: WriteAccess}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	handler := requireProjectFunc(parse, inner)

	tok := &db.APIToken{ID: 1, Scopes: "read"}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("POST", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireProject_WriteAccess_WrongProject_Returns403(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{Name: "pub", Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "pub", access: WriteAccess}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	handler := requireProjectFunc(parse, inner)

	otherProjectID := int64(999)
	tok := &db.APIToken{ID: 1, Scopes: "read,write", ProjectID: &otherProjectID}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("POST", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRequireProject_WriteAccess_ValidToken_PassesThrough(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{Name: "pub", Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "pub", access: WriteAccess}
	}

	var called bool
	var gotProject *db.Project
	var gotRI RouteInfo
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		gotProject = ProjectFrom(r.Context())
		gotRI = RouteInfoFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := requireProjectFunc(parse, inner)

	tok := &db.APIToken{ID: 1, Scopes: "read,write", ProjectID: &proj.ID}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("POST", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, gotProject)
	assert.Equal(t, "pub", gotProject.Name)
	require.NotNil(t, gotRI)
	assert.Equal(t, "pub", gotRI.ProjectName())
	assert.Equal(t, WriteAccess, gotRI.Access())
}

func TestRequireProject_AutoCreate_OIDCPrivate(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "docker-updater", access: WriteAccess}
	}

	var gotProject *db.Project
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProject = ProjectFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := requireProjectFunc(parse, inner)

	tok := &db.APIToken{ID: -1, Scopes: "read,write"}
	ctx := WithToken(context.Background(), tok)
	ctx = WithOIDCProject(ctx, "docker-updater")
	ctx = WithOIDCPrivate(ctx, true)
	req := httptest.NewRequest("PUT", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, gotProject)
	assert.Equal(t, "docker-updater", gotProject.Name)
	assert.True(t, gotProject.IsPrivate, "auto-created project should be private when OIDCPrivate is set")
}

func TestRequireProject_AutoCreate_OIDCPublic(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "public-repo", access: WriteAccess}
	}

	var gotProject *db.Project
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProject = ProjectFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := requireProjectFunc(parse, inner)

	tok := &db.APIToken{ID: -1, Scopes: "read,write"}
	ctx := WithToken(context.Background(), tok)
	ctx = WithOIDCProject(ctx, "public-repo")
	ctx = WithOIDCPrivate(ctx, false)
	req := httptest.NewRequest("PUT", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, gotProject)
	assert.Equal(t, "public-repo", gotProject.Name)
	assert.False(t, gotProject.IsPrivate, "auto-created project should be public when OIDCPrivate is not set")
}

func TestRequireProject_OIDCSyncsVisibility_PublicToPrivate(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{Name: "myrepo", Versioning: "auto", IsPrivate: false}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "myrepo", access: WriteAccess}
	}

	var gotProject *db.Project
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProject = ProjectFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := requireProjectFunc(parse, inner)

	tok := &db.APIToken{ID: -1, Scopes: "read,write"}
	ctx := WithToken(context.Background(), tok)
	ctx = WithOIDCProject(ctx, "myrepo")
	ctx = WithOIDCPrivate(ctx, true)
	req := httptest.NewRequest("PUT", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, gotProject)
	assert.True(t, gotProject.IsPrivate, "OIDC token should sync visibility from public to private")

	reloaded, err := d.GetProject(context.Background(), "myrepo")
	require.NoError(t, err)
	assert.True(t, reloaded.IsPrivate, "visibility change should be persisted in DB")
}

func TestRequireProject_OIDCSyncsVisibility_PrivateToPublic(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{Name: "myrepo2", Versioning: "auto", IsPrivate: true}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "myrepo2", access: WriteAccess}
	}

	var gotProject *db.Project
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProject = ProjectFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := requireProjectFunc(parse, inner)

	tok := &db.APIToken{ID: -1, Scopes: "read,write"}
	ctx := WithToken(context.Background(), tok)
	ctx = WithOIDCProject(ctx, "myrepo2")
	ctx = WithOIDCPrivate(ctx, false)
	req := httptest.NewRequest("PUT", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, gotProject)
	assert.False(t, gotProject.IsPrivate, "OIDC token should sync visibility from private to public")

	reloaded, err := d.GetProject(context.Background(), "myrepo2")
	require.NoError(t, err)
	assert.False(t, reloaded.IsPrivate, "visibility change should be persisted in DB")
}

func TestRequireProject_NonOIDCToken_CannotChangeVisibility(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{Name: "stable", Versioning: "auto", IsPrivate: false}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "stable", access: WriteAccess}
	}

	var gotProject *db.Project
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProject = ProjectFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := requireProjectFunc(parse, inner)

	tok := &db.APIToken{ID: 1, Scopes: "read,write", ProjectID: &proj.ID}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("PUT", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, gotProject)
	assert.False(t, gotProject.IsPrivate, "non-OIDC token must not change visibility")

	reloaded, err := d.GetProject(context.Background(), "stable")
	require.NoError(t, err)
	assert.False(t, reloaded.IsPrivate, "visibility must remain unchanged in DB")
}

func TestRequireProject_WrongOIDCProject_CannotChangeVisibility(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{Name: "target", Versioning: "auto", IsPrivate: false}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "target", access: WriteAccess}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	handler := requireProjectFunc(parse, inner)

	tok := &db.APIToken{ID: -1, Scopes: "read,write"}
	ctx := WithToken(context.Background(), tok)
	ctx = WithOIDCProject(ctx, "other-repo")
	ctx = WithOIDCPrivate(ctx, true)
	req := httptest.NewRequest("PUT", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)

	reloaded, err := d.GetProject(context.Background(), "target")
	require.NoError(t, err)
	assert.False(t, reloaded.IsPrivate, "wrong OIDC project token must not change visibility")
}
