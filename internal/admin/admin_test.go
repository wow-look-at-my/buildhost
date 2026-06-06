package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServer(t *testing.T) (*Server, *db.DB) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })

	cfg := config.Config{
		ListenAddr:		":8080",
		AdminListenAddr:	":9090",
		DataDir:		"./data",
	}
	build := BuildInfo{
		Version:	"v1.2.3",
		Commit:		"abc123def456789000aabbccdd",
		Date:		"2025-01-15T10:30:00Z",
		RepoURL:	"https://github.com/wow-look-at-my/buildhost",
	}
	srv := New(cfg, database, build)
	return srv, database
}

func serve(srv *Server, method, path string, body *bytes.Buffer) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, body)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	srv.NewHTTPServer().Handler.ServeHTTP(w, req)
	return w
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
		ReleaseID:	r.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind:	db.KindBinary, StorageKey: "abc123", Size: 2048, SHA256: "deadbeef",
	}
	require.NoError(t, database.CreateArtifact(ctx, a))

	_, _, err := database.CreateToken(ctx, "test-token", nil, "read,write")
	require.NoError(t, err)

	pid := p.ID
	require.NoError(t, database.CreateOIDCPolicy(ctx, &db.OIDCPolicy{
		Issuer:	"https://token.actions.githubusercontent.com", SubjectPattern: "repo:org/repo:*",
		ProjectID:	&pid, Scopes: "read,write",
	}))
}

func TestAPIDashboard(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	w := serve(srv, http.MethodGet, "/api/dashboard", nil)
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

	w := serve(srv, http.MethodGet, "/api/dashboard", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	recent := resp["recent"].([]any)
	assert.Empty(t, recent)
}

func TestAPIProjects(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	w := serve(srv, http.MethodGet, "/api/projects", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)
	assert.Equal(t, "testproject", resp[0]["name"])
	assert.Equal(t, float64(1), resp[0]["release_count"])
}

func TestAPIProjects_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/projects", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestAPIProject(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	w := serve(srv, http.MethodGet, "/api/projects/testproject", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	project := resp["project"].(map[string]any)
	assert.Equal(t, "testproject", project["name"])

	releases := resp["releases"].([]any)
	assert.Len(t, releases, 1)
	assert.Equal(t, "1.0.0", releases[0].(map[string]any)["version"])
}

func TestAPIProject_SlashNamespaced(t *testing.T) {
	srv, database := newTestServer(t)
	ctx := context.Background()
	p := &db.Project{Name: "cc-marketplace/recommend-go-toolchain", Versioning: db.VersioningAuto}
	require.NoError(t, database.CreateProject(ctx, p))

	w := serve(srv, http.MethodGet, "/api/projects/cc-marketplace/recommend-go-toolchain", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	project := resp["project"].(map[string]any)
	assert.Equal(t, "cc-marketplace/recommend-go-toolchain", project["name"])
}

func TestAPIProject_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/projects/nonexistent", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIRelease(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/testproject/releases/1.0.0", nil)
	req.Host = "buildhost.example.com"
	w := httptest.NewRecorder()
	srv.NewHTTPServer().Handler.ServeHTTP(w, req)

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
	// The admin dashboard runs on its own subdomain (buildhost.example.com here);
	// base_url is the registry root and service URLs are real per-service hosts.
	assert.Equal(t, "https://example.com", resp["base_url"])
	assertServiceURLs(t, resp)
}

// assertServiceURLs checks the "services" map carries the real per-service
// subdomain hosts the router serves, derived from the request Host
// (buildhost.example.com -> example.com) with the admin label stripped.
func assertServiceURLs(t *testing.T, resp map[string]any) {
	t.Helper()
	services, ok := resp["services"].(map[string]any)
	require.True(t, ok, "response is missing a services map")
	for _, svc := range []string{"dl", "apt", "brew", "npm", "oci", "sites", "static"} {
		assert.Equal(t, "https://"+svc+".example.com", services[svc])
	}
}

func TestAPIRelease_SlashNamespaced(t *testing.T) {
	srv, database := newTestServer(t)
	ctx := context.Background()
	p := &db.Project{Name: "cc-marketplace/recommend-go-toolchain", Versioning: db.VersioningAuto}
	require.NoError(t, database.CreateProject(ctx, p))
	r := &db.Release{ProjectID: p.ID, Version: "v1", VersionNum: 1}
	require.NoError(t, database.CreateRelease(ctx, r))

	w := serve(srv, http.MethodGet, "/api/projects/cc-marketplace/recommend-go-toolchain/releases/v1", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	project := resp["project"].(map[string]any)
	assert.Equal(t, "cc-marketplace/recommend-go-toolchain", project["name"])
	release := resp["release"].(map[string]any)
	assert.Equal(t, "v1", release["version"])
}

func TestAPIRelease_NotFoundProject(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/projects/nope/releases/1.0.0", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIRelease_NotFoundVersion(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	w := serve(srv, http.MethodGet, "/api/projects/testproject/releases/9.9.9", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIRegistries(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/api/registries", nil)
	req.Host = "buildhost.example.com"
	w := httptest.NewRecorder()
	srv.NewHTTPServer().Handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "https://example.com", resp["base_url"])
	assertServiceURLs(t, resp)

	projects := resp["projects"].([]any)
	assert.Len(t, projects, 1)
	assert.Equal(t, "testproject", projects[0].(map[string]any)["name"])
}

func TestAPICreateToken(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodPost, "/api/tokens", bytes.NewBufferString(`{"name":"mytoken","scopes":"read,write"}`))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["token"])
	details := resp["details"].(map[string]any)
	assert.Equal(t, "mytoken", details["name"])
	assert.Equal(t, "read,write", details["scopes"])
	assert.Equal(t, true, details["is_global"])
}

func TestAPICreateToken_ProjectScoped(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	ctx := context.Background()
	p, err := database.GetProject(ctx, "testproject")
	require.NoError(t, err)

	body := bytes.NewBufferString(fmt.Sprintf(`{"name":"proj-token","scopes":"read","project_id":%d}`, p.ID))
	w := serve(srv, http.MethodPost, "/api/tokens", body)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	details := resp["details"].(map[string]any)
	assert.Equal(t, "proj-token", details["name"])
	assert.Equal(t, false, details["is_global"])
}

func TestAPICreateToken_MissingName(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodPost, "/api/tokens", bytes.NewBufferString(`{"scopes":"read"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAPIUpdateToken(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	tokens, err := database.ListTokens(context.Background())
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	id := tokens[0].ID

	w := serve(srv, http.MethodPatch, "/api/tokens/"+fmt.Sprint(id), bytes.NewBufferString(`{"name":"renamed","scopes":"read"}`))
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestAPIUpdateToken_InvalidID(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodPatch, "/api/tokens/abc", bytes.NewBufferString(`{"name":"x","scopes":"read"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAPIDeleteToken(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	tokens, err := database.ListTokens(context.Background())
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	id := tokens[0].ID

	w := serve(srv, http.MethodDelete, "/api/tokens/"+fmt.Sprint(id), nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestAPIDeleteToken_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodDelete, "/api/tokens/9999", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIDeleteToken_InvalidID(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodDelete, "/api/tokens/abc", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAPITokens(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	w := serve(srv, http.MethodGet, "/api/tokens", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)
	assert.Equal(t, "test-token", resp[0]["name"])
	assert.Equal(t, "read,write", resp[0]["scopes"])
	assert.Equal(t, true, resp[0]["is_global"])
	assert.NotNil(t, resp[0]["id"])
}

func TestAPITokens_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/tokens", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestAPIOIDC(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	w := serve(srv, http.MethodGet, "/api/oidc", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)
	assert.Contains(t, resp[0]["issuer"], "token.actions.githubusercontent.com")
	assert.Equal(t, "testproject", resp[0]["project_name"])
}

func TestAPIOIDC_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/oidc", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestAPISites(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	ctx := context.Background()
	p, _ := database.GetProject(ctx, "testproject")
	_, err := database.UpsertSite(ctx, &db.Site{
		ProjectID:	p.ID, Branch: "main", StorageKey: "sitekey1",
		Size:	4096, SHA256: "sitehash", FileCount: 10, GitCommit: "abc123",
	})
	require.NoError(t, err)

	w := serve(srv, http.MethodGet, "/api/sites", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	sites := resp["sites"].([]any)
	assert.Len(t, sites, 1)
	assert.Equal(t, "testproject", sites[0].(map[string]any)["project_name"])
	assert.Equal(t, "main", sites[0].(map[string]any)["branch"])
}

func TestAPISites_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/sites", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	sites := resp["sites"].([]any)
	assert.Empty(t, sites)
}

func TestAPIProject_IncludesSites(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	ctx := context.Background()
	p, _ := database.GetProject(ctx, "testproject")
	_, err := database.UpsertSite(ctx, &db.Site{
		ProjectID:	p.ID, Branch: "dev", StorageKey: "sitekey2",
		Size:	2048, SHA256: "sitehash2", FileCount: 5,
	})
	require.NoError(t, err)

	w := serve(srv, http.MethodGet, "/api/projects/testproject", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	sites := resp["sites"].([]any)
	assert.Len(t, sites, 1)
	assert.Equal(t, "dev", sites[0].(map[string]any)["branch"])
}

func TestAPIArtifacts(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	w := serve(srv, http.MethodGet, "/api/artifacts", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)
	assert.Equal(t, "testproject", resp[0]["project_name"])
	assert.Equal(t, "1.0.0", resp[0]["version"])
	assert.Equal(t, "linux", resp[0]["os"])
	assert.Equal(t, "amd64", resp[0]["arch"])
}

func TestAPIArtifacts_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/artifacts", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestAPIStorage(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	w := serve(srv, http.MethodGet, "/api/storage", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	projects := resp["projects"].([]any)
	assert.Len(t, projects, 1)
	assert.Equal(t, "testproject", projects[0].(map[string]any)["name"])
	assert.Equal(t, float64(2048), projects[0].(map[string]any)["total_bytes"])
	assert.Equal(t, float64(2048), resp["total_bytes"])
}

func TestAPIStorage_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/storage", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	projects := resp["projects"].([]any)
	assert.Empty(t, projects)
}

func TestAPISidebar(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/sidebar", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	build := resp["build"].(map[string]any)
	assert.Equal(t, "v1.2.3", build["version"])
	assert.Equal(t, "abc123def456", build["short_commit"])
	assert.Contains(t, resp["cpu_percent"], "%")
}

func TestNewHTTPServer(t *testing.T) {
	srv, _ := newTestServer(t)
	httpSrv := srv.NewHTTPServer()

	assert.Equal(t, ":9090", httpSrv.Addr)
	assert.NotNil(t, httpSrv.Handler)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	w := httptest.NewRecorder()
	httpSrv.Handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
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
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
	assert.Equal(t, "max-age=63072000; includeSubDomains", w.Header().Get("Strict-Transport-Security"))
	assert.Equal(t, "default-src 'self' data: 'unsafe-inline'", w.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "none", w.Header().Get("X-Permitted-Cross-Domain-Policies"))
	assert.Equal(t, "interest-cohort=()", w.Header().Get("Permissions-Policy"))
}

func TestServeSPA_StaticFile(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/style.css", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "--sidebar-bg")
}

func TestServeSPA_Fallback(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/projects/anything", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, w.Body.String(), "Buildhost Admin")
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		input	int64
		want	string
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
		input	time.Duration
		want	string
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
		ago	time.Duration
		want	string
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
