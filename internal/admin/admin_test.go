package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

func seedData(t *testing.T, database *db.DB) {
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

	_, _, err := database.CreateToken(ctx, "test-token", nil, "read,write")
	require.NoError(t, err)

	pid := p.ID
	require.NoError(t, database.CreateOIDCPolicy(ctx, &model.OIDCPolicy{
		Issuer: "https://token.actions.githubusercontent.com", SubjectPattern: "repo:org/repo:*",
		ProjectID: &pid, Scopes: "read,write",
	}))
}

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
