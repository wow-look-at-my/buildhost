package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	return d
}

func TestAuthenticate_NoToken_PassesThrough(t *testing.T) {
	d := openTestDB(t)
	m := &Middleware{DB: d}

	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// No token should be in context.
		assert.Nil(t, TokenFrom(r.Context()))
		w.WriteHeader(http.StatusOK)
	})

	handler := m.Authenticate(inner)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAuthenticate_ValidBearerToken_SetsContext(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	// Create a real token in the DB.
	plaintext, _, err := d.CreateToken(ctx, "test-token", nil, "read,write")
	require.NoError(t, err)

	m := &Middleware{DB: d}

	var gotToken *model.APIToken
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = TokenFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := m.Authenticate(inner)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, gotToken)
	assert.Equal(t, "test-token", gotToken.Name)
	assert.Equal(t, "read,write", gotToken.Scopes)
}

func TestAuthenticate_InvalidToken_PassesThroughWithoutContext(t *testing.T) {
	d := openTestDB(t)
	m := &Middleware{DB: d}

	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		assert.Nil(t, TokenFrom(r.Context()))
		w.WriteHeader(http.StatusOK)
	})

	handler := m.Authenticate(inner)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer invalid-token-value")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireWrite_NoToken_Returns401(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	handler := RequireWrite(inner)

	req := httptest.NewRequest("POST", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "authentication required")
}

func TestRequireWrite_TokenWithoutWriteScope_Returns401(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	handler := RequireWrite(inner)

	// Set a token with only "read" scope.
	tok := &model.APIToken{ID: 1, Scopes: "read"}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("POST", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "authentication required")
}

func TestRequireWrite_TokenWithWriteScope_PassesThrough(t *testing.T) {
	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := RequireWrite(inner)

	tok := &model.APIToken{ID: 1, Scopes: "read,write"}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("POST", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// testRouteInfo implements RouteInfo for test purposes.
type testRouteInfo struct {
	project string
	access  AccessLevel
}

func (r testRouteInfo) ProjectName() string { return r.project }
func (r testRouteInfo) Access() AccessLevel { return r.access }

// initTestMiddleware sets up the package-level mw variable for tests.
func initTestMiddleware(t *testing.T, d *db.DB) {
	t.Helper()
	mw = &Middleware{DB: d}
	t.Cleanup(func() { mw = nil })
}

func TestRequireProject_PublicProject_ReadAccess_PassesThrough(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &model.Project{Name: "pub", Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "pub", access: ReadAccess}
	}

	var called bool
	var gotProject *model.Project
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		gotProject = ProjectFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := requireProjectFunc(parse, inner)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, gotProject)
	assert.Equal(t, "pub", gotProject.Name)
}

func TestRequireProject_NonexistentProject_Returns404(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "nosuch", access: ReadAccess}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	handler := requireProjectFunc(parse, inner)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRequireProject_EmptyProjectName_Returns404(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "", access: ReadAccess}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	handler := requireProjectFunc(parse, inner)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRequireProject_PrivateProject_NoToken_Returns401(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "secret", access: ReadAccess}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	handler := requireProjectFunc(parse, inner)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireProject_PrivateProject_WrongProjectToken_Returns403(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "secret", access: ReadAccess}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	handler := requireProjectFunc(parse, inner)

	otherProjectID := int64(999)
	tok := &model.APIToken{ID: 1, Scopes: "read", ProjectID: &otherProjectID}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "not authorized for this project")
}

func TestRequireProject_PrivateProject_CorrectToken_PassesThrough(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "secret", access: ReadAccess}
	}

	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := requireProjectFunc(parse, inner)

	tok := &model.APIToken{ID: 1, Scopes: "read", ProjectID: &proj.ID}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireProject_PrivateProject_GlobalToken_PassesThrough(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "secret", access: ReadAccess}
	}

	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := requireProjectFunc(parse, inner)

	// Global token (nil ProjectID) should be allowed.
	tok := &model.APIToken{ID: 1, Scopes: "read"}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireProject_PrivateProject_WriteOnlyScope_Returns401(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "secret", access: ReadAccess}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	handler := requireProjectFunc(parse, inner)

	// Token with write-only scope should be rejected for read access.
	tok := &model.APIToken{ID: 1, Scopes: "write", ProjectID: &proj.ID}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireProject_WriteAccess_NoToken_Returns401(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &model.Project{Name: "pub", Versioning: "auto"}
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

	proj := &model.Project{Name: "pub", Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "pub", access: WriteAccess}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	handler := requireProjectFunc(parse, inner)

	tok := &model.APIToken{ID: 1, Scopes: "read"}
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

	proj := &model.Project{Name: "pub", Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "pub", access: WriteAccess}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	handler := requireProjectFunc(parse, inner)

	otherProjectID := int64(999)
	tok := &model.APIToken{ID: 1, Scopes: "read,write", ProjectID: &otherProjectID}
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

	proj := &model.Project{Name: "pub", Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "pub", access: WriteAccess}
	}

	var called bool
	var gotProject *model.Project
	var gotRI RouteInfo
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		gotProject = ProjectFrom(r.Context())
		gotRI = RouteInfoFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := requireProjectFunc(parse, inner)

	tok := &model.APIToken{ID: 1, Scopes: "read,write", ProjectID: &proj.ID}
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

	var gotProject *model.Project
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProject = ProjectFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := requireProjectFunc(parse, inner)

	tok := &model.APIToken{ID: -1, Scopes: "read,write", OIDCProject: "docker-updater", OIDCPrivate: true}
	ctx := WithToken(context.Background(), tok)
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

	var gotProject *model.Project
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProject = ProjectFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := requireProjectFunc(parse, inner)

	tok := &model.APIToken{ID: -1, Scopes: "read,write", OIDCProject: "public-repo", OIDCPrivate: false}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("PUT", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, gotProject)
	assert.Equal(t, "public-repo", gotProject.Name)
	assert.False(t, gotProject.IsPrivate, "auto-created project should be public when OIDCPrivate is not set")
}

func TestRequireProject_PrivateProject_OCI_Returns401WithWWWAuthenticate(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "secret", access: ReadAccess}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	handler := requireProjectFunc(parse, inner)

	req := httptest.NewRequest("GET", "/v2/secret/manifests/latest", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, `Basic realm="buildhost"`, rec.Header().Get("Www-Authenticate"))
	assert.Contains(t, rec.Body.String(), "UNAUTHORIZED")
}

func TestRequireProject_PrivateProject_NonOCI_NoWWWAuthenticate(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: "auto"}
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
	assert.Empty(t, rec.Header().Get("Www-Authenticate"))
	assert.Contains(t, rec.Body.String(), "authentication required")
}
