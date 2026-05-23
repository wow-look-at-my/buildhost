package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

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

func TestAPIDashboard(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	w := httptest.NewRecorder()
	srv.apiDashboard(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	stats := resp["stats"].(map[string]any)
	assert.Equal(t, float64(1), stats["project_count"])
	assert.Equal(t, float64(1), stats["release_count"])
	assert.Equal(t, float64(1), stats["artifact_count"])
	assert.Equal(t, float64(2048), stats["total_storage_bytes"])

	build := resp["build"].(map[string]any)
	assert.Equal(t, "v1.2.3", build["version"])
	assert.Equal(t, "abc123def456", build["short_commit"])
	assert.Contains(t, build["commit_url"], "github.com/wow-look-at-my/buildhost/commit/abc123def456789000aabbccdd")

	recent := resp["recent"].([]any)
	assert.Len(t, recent, 1)
	assert.Equal(t, "testproject", recent[0].(map[string]any)["project_name"])

	assert.NotEmpty(t, resp["uptime"])
	assert.Contains(t, resp["cpu_percent"], "%")
}

func TestAPIDashboard_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	w := httptest.NewRecorder()
	srv.apiDashboard(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	recent := resp["recent"].([]any)
	assert.Empty(t, recent)
}

func TestAPIProjects(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	w := httptest.NewRecorder()
	srv.apiProjects(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)
	assert.Equal(t, "testproject", resp[0]["name"])
	assert.Equal(t, float64(1), resp[0]["release_count"])
}

func TestAPIProjects_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	w := httptest.NewRecorder()
	srv.apiProjects(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestAPIProject(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/testproject", nil)
	req.SetPathValue("name", "testproject")
	w := httptest.NewRecorder()
	srv.apiProject(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	project := resp["project"].(map[string]any)
	assert.Equal(t, "testproject", project["name"])

	releases := resp["releases"].([]any)
	assert.Len(t, releases, 1)
	assert.Equal(t, "1.0.0", releases[0].(map[string]any)["version"])
}

func TestAPIProject_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	srv.apiProject(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIRelease(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/testproject/releases/1.0.0", nil)
	req.SetPathValue("name", "testproject")
	req.SetPathValue("version", "1.0.0")
	w := httptest.NewRecorder()
	srv.apiRelease(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	project := resp["project"].(map[string]any)
	assert.Equal(t, "testproject", project["name"])

	release := resp["release"].(map[string]any)
	assert.Equal(t, "1.0.0", release["version"])

	artifacts := resp["artifacts"].([]any)
	assert.Len(t, artifacts, 1)
	a := artifacts[0].(map[string]any)
	assert.Equal(t, "linux", a["os"])
	assert.Equal(t, "amd64", a["arch"])
	assert.Equal(t, float64(2048), a["size"])

	assert.Equal(t, float64(2048), resp["total_size"])
	assert.Equal(t, "http://localhost:8080", resp["base_url"])
}

func TestAPIRelease_NotFoundProject(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/nope/releases/1.0.0", nil)
	req.SetPathValue("name", "nope")
	req.SetPathValue("version", "1.0.0")
	w := httptest.NewRecorder()
	srv.apiRelease(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIRelease_NotFoundVersion(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/testproject/releases/9.9.9", nil)
	req.SetPathValue("name", "testproject")
	req.SetPathValue("version", "9.9.9")
	w := httptest.NewRecorder()
	srv.apiRelease(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIRegistries(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/api/registries", nil)
	w := httptest.NewRecorder()
	srv.apiRegistries(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "http://localhost:8080", resp["base_url"])

	projects := resp["projects"].([]any)
	assert.Len(t, projects, 1)
	assert.Equal(t, "testproject", projects[0].(map[string]any)["name"])
}

func TestAPITokens(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/api/tokens", nil)
	w := httptest.NewRecorder()
	srv.apiTokens(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)
	assert.Equal(t, "test-token", resp[0]["name"])
	assert.Equal(t, "read,write", resp[0]["scopes"])
	assert.Equal(t, true, resp[0]["is_global"])
}

func TestAPITokens_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/tokens", nil)
	w := httptest.NewRecorder()
	srv.apiTokens(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestAPIOIDC(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/api/oidc", nil)
	w := httptest.NewRecorder()
	srv.apiOIDC(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)
	assert.Contains(t, resp[0]["issuer"], "token.actions.githubusercontent.com")
	assert.Equal(t, "testproject", resp[0]["project_name"])
}

func TestAPIOIDC_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/oidc", nil)
	w := httptest.NewRecorder()
	srv.apiOIDC(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestAPISidebar(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/sidebar", nil)
	w := httptest.NewRecorder()
	srv.apiSidebar(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	build := resp["build"].(map[string]any)
	assert.Equal(t, "v1.2.3", build["version"])
	assert.Equal(t, "abc123def456", build["short_commit"])
	assert.Contains(t, resp["cpu_percent"], "%")
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

func TestServeSPA_StaticFile(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/style.css", nil)
	w := httptest.NewRecorder()
	srv.serveSPA(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "--sidebar-bg")
}

func TestServeSPA_Fallback(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/projects/anything", nil)
	w := httptest.NewRecorder()
	srv.serveSPA(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, w.Body.String(), "Buildhost Admin")
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
