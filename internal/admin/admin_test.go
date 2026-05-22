package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
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
	build := BuildInfo{
		Version: "v1.2.3",
		Commit:  "abc123def456789000aabbccdd",
		Date:    "2025-01-15T10:30:00Z",
		RepoURL: "https://github.com/wow-look-at-my/buildhost",
	}
	srv := New(cfg, database, build)
	return srv, database
}

func seedData(t *testing.T, database *db.DB) {
	t.Helper()
	ctx := context.Background()

	p := &db.Project{Name: "testproject", Description: "A test project", Versioning: db.VersioningAuto}
	require.NoError(t, database.CreateProject(ctx, p))

	r := &db.Release{ProjectID: p.ID, Version: "1.0.0", VersionNum: 1, GitBranch: "main"}
	require.NoError(t, database.CreateRelease(ctx, r))
	require.NoError(t, database.PublishRelease(ctx, r.ID))

	a := &db.Artifact{
		ReleaseID: r.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: "abc123", Size: 2048, SHA256: "deadbeef",
	}
	require.NoError(t, database.CreateArtifact(ctx, a))

	_, _, err := database.CreateToken(ctx, "test-token", nil, "read,write")
	require.NoError(t, err)

	pid := p.ID
	require.NoError(t, database.CreateOIDCPolicy(ctx, &db.OIDCPolicy{
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
	assert.Contains(t, body, "Server Status")
	assert.Contains(t, body, "v1.2.3")
	assert.Contains(t, body, "abc123def456")
	assert.Contains(t, body, "2025-01-15T10:30:00Z")
	assert.Contains(t, body, "github.com/wow-look-at-my/buildhost/commit/abc123def456789000aabbccdd")
	assert.Contains(t, body, "Uptime")
	assert.Contains(t, body, "CPU Usage")
	assert.Contains(t, body, "CPU Time")
	assert.Contains(t, body, "Configuration")
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

func TestReleaseHandler(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/projects/testproject/releases/1.0.0", nil)
	req.SetPathValue("name", "testproject")
	req.SetPathValue("version", "1.0.0")
	w := httptest.NewRecorder()
	srv.handleRelease(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "testproject")
	assert.Contains(t, body, "1.0.0")
	assert.Contains(t, body, "linux/amd64")
	assert.Contains(t, body, "2.0 KiB")
	assert.Contains(t, body, "Download Endpoints")
	assert.Contains(t, body, "/dl/testproject/1.0.0/")
	assert.Contains(t, body, "/apt/testproject")
	assert.Contains(t, body, "/brew/testproject.rb")
	assert.Contains(t, body, "/npm/@buildhost/testproject")
}

func TestReleaseHandler_NotFoundProject(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/projects/nope/releases/1.0.0", nil)
	req.SetPathValue("name", "nope")
	req.SetPathValue("version", "1.0.0")
	w := httptest.NewRecorder()
	srv.handleRelease(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestReleaseHandler_NotFoundVersion(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/projects/testproject/releases/9.9.9", nil)
	req.SetPathValue("name", "testproject")
	req.SetPathValue("version", "9.9.9")
	w := httptest.NewRecorder()
	srv.handleRelease(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRegistriesHandler(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/registries", nil)
	w := httptest.NewRecorder()
	srv.handleRegistries(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Registry Endpoints")
	assert.Contains(t, body, "Direct Downloads")
	assert.Contains(t, body, "APT Repository")
	assert.Contains(t, body, "Homebrew Tap")
	assert.Contains(t, body, "npm Registry")
	assert.Contains(t, body, "OCI Distribution")
	assert.Contains(t, body, "REST API")
	assert.Contains(t, body, "testproject")
	assert.Contains(t, body, "localhost:8080")
}

func TestRegistriesHandler_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/registries", nil)
	w := httptest.NewRecorder()
	srv.handleRegistries(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Registry Endpoints")
	assert.NotContains(t, body, "Quick links")
}

func TestSecurityHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := securityHeaders(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "SAMEORIGIN", w.Header().Get("X-Frame-Options"))
	assert.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
	assert.Equal(t, "default-src 'self'", w.Header().Get("Content-Security-Policy"))
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

func TestBuildInfo_CommitURL(t *testing.T) {
	b := BuildInfo{Commit: "abc123", RepoURL: "https://github.com/org/repo"}
	assert.Equal(t, "https://github.com/org/repo/commit/abc123", b.CommitURL())

	assert.Equal(t, "", BuildInfo{Commit: "none", RepoURL: "https://github.com/org/repo"}.CommitURL())
	assert.Equal(t, "", BuildInfo{Commit: "", RepoURL: "https://github.com/org/repo"}.CommitURL())
	assert.Equal(t, "", BuildInfo{Commit: "abc123", RepoURL: ""}.CommitURL())
}

func TestBuildInfo_ShortCommit(t *testing.T) {
	assert.Equal(t, "abc123def456", BuildInfo{Commit: "abc123def456789000aabbccdd"}.ShortCommit())
	assert.Equal(t, "short", BuildInfo{Commit: "short"}.ShortCommit())
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		input time.Duration
		want  string
	}{
		{0, "0s"},
		{500 * time.Millisecond, "0s"},
		{30 * time.Second, "0m 30s"},
		{90 * time.Second, "1m 30s"},
		{3661 * time.Second, "1h 1m 1s"},
		{90061 * time.Second, "1d 1h 1m"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, formatDuration(tc.input))
	}
}

func TestTimeAgo(t *testing.T) {
	tests := []struct {
		ago  time.Duration
		want string
	}{
		{0, "just now"},
		{30 * time.Second, "just now"},
		{1 * time.Minute, "1 minute ago"},
		{5 * time.Minute, "5 minutes ago"},
		{1 * time.Hour, "1 hour ago"},
		{3 * time.Hour, "3 hours ago"},
		{24 * time.Hour, "1 day ago"},
		{72 * time.Hour, "3 days ago"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, timeAgo(time.Now().Add(-tc.ago)))
	}
}

func TestGetCPUTime(t *testing.T) {
	d := getCPUTime()
	assert.True(t, d >= 0, "CPU time should be non-negative")
}
