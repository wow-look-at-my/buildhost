package admin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

func newTestServer(t *testing.T) (*Server, *db.DB) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })

	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)

	cfg := config.Config{
		ListenAddr:      ":8080",
		AdminListenAddr: ":9090",
		DataDir:         "./data",
	}
	build := BuildInfo{
		Version: "v1.2.3",
		Commit:  "abc123def456789000aabbccdd",
		Date:    "2025-01-15T10:30:00Z",
		RepoURL: "https://github.com/wow-look-at-my/buildhost",
	}
	srv := New(cfg, database, store, build)
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

func TestNewHTTPServer(t *testing.T) {
	t.Serial()
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
	t.Serial()
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
	t.Serial()
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/style.css", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "--sidebar-bg")
}

func TestServeSPA_Fallback(t *testing.T) {
	t.Serial()
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/projects/anything", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, w.Body.String(), "Buildhost Admin")
}

func TestHumanSize(t *testing.T) {
	t.Serial()
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
	t.Serial()
	assert.Equal(t, "-", formatTimePtr(nil))
}

func TestBuildInfo_CommitURL(t *testing.T) {
	t.Serial()
	b := BuildInfo{Commit: "abc123", RepoURL: "https://github.com/org/repo"}
	assert.Equal(t, "https://github.com/org/repo/commit/abc123", b.CommitURL())

	assert.Equal(t, "", BuildInfo{Commit: "none", RepoURL: "https://github.com/org/repo"}.CommitURL())
	assert.Equal(t, "", BuildInfo{Commit: "", RepoURL: "https://github.com/org/repo"}.CommitURL())
	assert.Equal(t, "", BuildInfo{Commit: "abc123", RepoURL: ""}.CommitURL())
}

func TestBuildInfo_ShortCommit(t *testing.T) {
	t.Serial()
	assert.Equal(t, "abc123def456", BuildInfo{Commit: "abc123def456789000aabbccdd"}.ShortCommit())
	assert.Equal(t, "short", BuildInfo{Commit: "short"}.ShortCommit())
}

func TestFormatDuration(t *testing.T) {
	t.Serial()
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
	t.Serial()
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
	t.Serial()
	d := getCPUTime()
	assert.True(t, d >= 0, "CPU time should be non-negative")
}
