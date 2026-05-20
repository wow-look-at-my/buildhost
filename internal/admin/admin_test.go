package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func newTestServer(t *testing.T) (*Server, *db.DB) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })

	cfg := config.Config{
		ListenAddr:      ":8080",
		AdminListenAddr: ":9090",
		DataDir:         "./data",
		BaseURL:         "http://localhost:8080",
	}
	srv := New(cfg, database)
	return srv, database
}

func seedData(t *testing.T, database *db.DB) string {
	t.Helper()
	ctx := context.Background()

	p := &model.Project{Name: "testproject", Description: "A test project", Versioning: model.VersioningAuto}
	require.NoError(t, database.CreateProject(ctx, p))

	r := &model.Release{ProjectID: p.ID, Version: "1.0.0", VersionNum: 1, GitBranch: "main"}
	require.NoError(t, database.CreateRelease(ctx, r))
	require.NoError(t, database.PublishRelease(ctx, r.ID))

	a := &model.Artifact{
		ReleaseID: r.ID, OS: model.OSLinux, Arch: model.ArchAMD64,
		Kind: model.KindBinary, StorageKey: "abc123", Size: 2048, SHA256: "deadbeef",
	}
	require.NoError(t, database.CreateArtifact(ctx, a))

	plaintext, _, err := database.CreateToken(ctx, "test-token", nil, "read,write")
	require.NoError(t, err)

	pid := p.ID
	require.NoError(t, database.CreateOIDCPolicy(ctx, &model.OIDCPolicy{
		Issuer: "https://token.actions.githubusercontent.com", SubjectPattern: "repo:org/repo:*",
		ProjectID: &pid, Scopes: "read,write",
	}))

	return plaintext
}

func authedRequest(method, path, token string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	return req
}

// --- Auth middleware ---

func TestAuthMiddleware_RedirectsToLogin(t *testing.T) {
	srv, _ := newTestServer(t)

	handler := srv.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/login", w.Header().Get("Location"))
}

func TestAuthMiddleware_AllowsStaticWithoutAuth(t *testing.T) {
	srv, _ := newTestServer(t)

	handler := srv.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_AllowsLoginWithoutAuth(t *testing.T) {
	srv, _ := newTestServer(t)

	handler := srv.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_ValidCookie(t *testing.T) {
	srv, database := newTestServer(t)
	token := seedData(t, database)

	handler := srv.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	req := authedRequest(http.MethodGet, "/", token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_ValidBearerHeader(t *testing.T) {
	srv, database := newTestServer(t)
	token := seedData(t, database)

	handler := srv.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_InvalidToken_RedirectsToLogin(t *testing.T) {
	srv, _ := newTestServer(t)

	handler := srv.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := authedRequest(http.MethodGet, "/", "bh_bogus_token_value")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/login", w.Header().Get("Location"))
}

func TestAuthMiddleware_ReadOnlyToken_RedirectsToLogin(t *testing.T) {
	srv, database := newTestServer(t)
	ctx := context.Background()
	plaintext, _, err := database.CreateToken(ctx, "readonly", nil, "read")
	require.NoError(t, err)

	handler := srv.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := authedRequest(http.MethodGet, "/", plaintext)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
}

func TestAuthMiddleware_ProjectScopedToken_RedirectsToLogin(t *testing.T) {
	srv, database := newTestServer(t)
	ctx := context.Background()
	p := &model.Project{Name: "scoped", Versioning: model.VersioningAuto}
	require.NoError(t, database.CreateProject(ctx, p))
	pid := p.ID
	plaintext, _, err := database.CreateToken(ctx, "projtoken", &pid, "read,write")
	require.NoError(t, err)

	handler := srv.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := authedRequest(http.MethodGet, "/", plaintext)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
}

// --- Login ---

func TestLoginPage(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	w := httptest.NewRecorder()
	srv.handleLoginPage(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Sign in")
	assert.Contains(t, w.Body.String(), "API Token")
}

func TestLoginSubmit_Success(t *testing.T) {
	srv, database := newTestServer(t)
	token := seedData(t, database)

	form := url.Values{"token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleLoginSubmit(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/", w.Header().Get("Location"))

	cookies := w.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == cookieName {
			assert.Equal(t, token, c.Value)
			assert.True(t, c.HttpOnly)
			found = true
		}
	}
	assert.True(t, found)
}

func TestLoginSubmit_EmptyToken(t *testing.T) {
	srv, _ := newTestServer(t)

	form := url.Values{"token": {""}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleLoginSubmit(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Token is required")
}

func TestLoginSubmit_InvalidToken(t *testing.T) {
	srv, _ := newTestServer(t)

	form := url.Values{"token": {"bh_invalid_token_value"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleLoginSubmit(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid token")
}

func TestLogout(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/logout", nil)
	w := httptest.NewRecorder()
	srv.handleLogout(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/login", w.Header().Get("Location"))

	cookies := w.Result().Cookies()
	for _, c := range cookies {
		if c.Name == cookieName {
			assert.True(t, c.MaxAge < 0)
		}
	}
}

// --- Page handlers (test directly, bypassing auth) ---

func TestDashboardHandler(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.handleDashboard(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Dashboard")
	assert.Contains(t, body, "Projects")
	assert.Contains(t, body, "2.0 KiB")
}

func TestDashboardHandler_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.handleDashboard(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "No releases yet")
}

func TestProjectsHandler(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/projects", nil)
	w := httptest.NewRecorder()
	srv.handleProjects(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "testproject")
	assert.Contains(t, body, "auto")
}

func TestProjectsHandler_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/projects", nil)
	w := httptest.NewRecorder()
	srv.handleProjects(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "No projects yet")
}

func TestProjectHandler(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/projects/testproject", nil)
	req.SetPathValue("name", "testproject")
	w := httptest.NewRecorder()
	srv.handleProject(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "testproject")
	assert.Contains(t, body, "1.0.0")
	assert.Contains(t, body, "Published")
}

func TestProjectHandler_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/projects/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	srv.handleProject(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestTokensHandler(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/tokens", nil)
	w := httptest.NewRecorder()
	srv.handleTokens(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "test-token")
	assert.Contains(t, body, "read,write")
}

func TestTokensHandler_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/tokens", nil)
	w := httptest.NewRecorder()
	srv.handleTokens(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "No tokens yet")
}

func TestOIDCHandler(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/oidc", nil)
	w := httptest.NewRecorder()
	srv.handleOIDCPolicies(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "token.actions.githubusercontent.com")
	assert.Contains(t, body, "testproject")
}

func TestOIDCHandler_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/oidc", nil)
	w := httptest.NewRecorder()
	srv.handleOIDCPolicies(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "No OIDC policies configured")
}

func TestStaticFiles(t *testing.T) {
	srv, _ := newTestServer(t)

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServerFS(content))

	req := httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "--sidebar-bg")
	_ = srv
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1048576, "1.0 MiB"},
		{1073741824, "1.0 GiB"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, humanSize(tc.input))
	}
}

func TestFormatTimePtr_Nil(t *testing.T) {
	assert.Equal(t, "-", formatTimePtr(nil))
}
