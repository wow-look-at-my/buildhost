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

func TestRequireReadForProject_PublicProject_PassesThrough(t *testing.T) {
	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	getProject := func(r *http.Request) *model.Project {
		return &model.Project{ID: 1, IsPrivate: false}
	}

	handler := RequireReadForProject(inner, getProject)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireReadForProject_PrivateProject_NoToken_Returns401(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	getProject := func(r *http.Request) *model.Project {
		return &model.Project{ID: 1, IsPrivate: true}
	}

	handler := RequireReadForProject(inner, getProject)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireReadForProject_PrivateProject_WrongProjectToken_Returns403(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	getProject := func(r *http.Request) *model.Project {
		return &model.Project{ID: 1, IsPrivate: true}
	}

	handler := RequireReadForProject(inner, getProject)

	otherProjectID := int64(99)
	tok := &model.APIToken{ID: 1, Scopes: "read", ProjectID: &otherProjectID}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "not authorized for this project")
}

func TestRequireReadForProject_PrivateProject_CorrectToken_PassesThrough(t *testing.T) {
	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	getProject := func(r *http.Request) *model.Project {
		return &model.Project{ID: 1, IsPrivate: true}
	}

	handler := RequireReadForProject(inner, getProject)

	projectID := int64(1)
	tok := &model.APIToken{ID: 1, Scopes: "read", ProjectID: &projectID}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestEnforceProjectRead_PublicProject_OK(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	project := &model.Project{ID: 1, IsPrivate: false}
	status, ok := EnforceProjectRead(req, project)
	assert.True(t, ok)
	assert.Equal(t, 0, status)
}

func TestEnforceProjectRead_NilProject_OK(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	status, ok := EnforceProjectRead(req, nil)
	assert.True(t, ok)
	assert.Equal(t, 0, status)
}

func TestEnforceProjectRead_PrivateNoToken_401(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	project := &model.Project{ID: 1, IsPrivate: true}
	status, ok := EnforceProjectRead(req, project)
	assert.False(t, ok)
	assert.Equal(t, 401, status)
}

func TestEnforceProjectRead_PrivateWrongProject_403(t *testing.T) {
	otherID := int64(99)
	tok := &model.APIToken{ID: 1, Scopes: "read", ProjectID: &otherID}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(ctx)
	project := &model.Project{ID: 1, IsPrivate: true}
	status, ok := EnforceProjectRead(req, project)
	assert.False(t, ok)
	assert.Equal(t, 403, status)
}

func TestEnforceProjectRead_PrivateCorrectToken_OK(t *testing.T) {
	projID := int64(1)
	tok := &model.APIToken{ID: 1, Scopes: "read", ProjectID: &projID}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(ctx)
	project := &model.Project{ID: 1, IsPrivate: true}
	status, ok := EnforceProjectRead(req, project)
	assert.True(t, ok)
	assert.Equal(t, 0, status)
}

func TestEnforceProjectRead_PrivateGlobalToken_OK(t *testing.T) {
	tok := &model.APIToken{ID: 1, Scopes: "read"}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(ctx)
	project := &model.Project{ID: 1, IsPrivate: true}
	status, ok := EnforceProjectRead(req, project)
	assert.True(t, ok)
	assert.Equal(t, 0, status)
}

func TestEnforceProjectRead_PrivateWriteOnlyScope_401(t *testing.T) {
	tok := &model.APIToken{ID: 1, Scopes: "write"}
	ctx := WithToken(context.Background(), tok)
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(ctx)
	project := &model.Project{ID: 1, IsPrivate: true}
	status, ok := EnforceProjectRead(req, project)
	assert.False(t, ok)
	assert.Equal(t, 401, status)
}
