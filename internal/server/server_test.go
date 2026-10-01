package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	_ "github.com/wow-look-at-my/buildhost/internal/api"
	_ "github.com/wow-look-at-my/buildhost/internal/apt"
	_ "github.com/wow-look-at-my/buildhost/internal/brew"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	_ "github.com/wow-look-at-my/buildhost/internal/dl"
	_ "github.com/wow-look-at-my/buildhost/internal/llms"
	_ "github.com/wow-look-at-my/buildhost/internal/npm"
	_ "github.com/wow-look-at-my/buildhost/internal/oci"
	"github.com/wow-look-at-my/buildhost/internal/server"
	_ "github.com/wow-look-at-my/buildhost/internal/sites"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// testEnv bundles the objects needed by every integration test. cfg, store,
// and handler are kept so a test can simulate a redeploy (server.New over the
type testEnv struct {
	ts       *httptest.Server
	database *db.DB
	token    string // plaintext API token with read,write scopes
	cfg      config.Config
	store    storage.Storage
	handler  http.Handler
}

func setup(t *testing.T) *testEnv { return setupWith(t, nil) }

// setupWith boots a full server whose config was adjusted by mutate (nil for
// the defaults) -- e.g. the site-domain tests set cfg.SiteDomain/PrimaryDomain.
func setupWith(t *testing.T, mutate func(*config.Config)) *testEnv {
	t.Helper()

	dbDir := t.TempDir()
	storeDir := t.TempDir()

	dbPath := filepath.Join(dbDir, "test.db")
	database, err := db.Open(dbPath)
	require.Nil(t, err)

	t.Cleanup(func() { database.Close() })

	store, err := storage.NewFilesystem(storeDir, true)
	require.Nil(t, err)

	cfg := config.Config{
		ListenAddr: ":0",
		DataDir:    dbDir,
		DBPath:     dbPath,
	}
	if mutate != nil {
		mutate(&cfg)
	}

	srv := server.New(cfg, database, store)
	handler := srv.Handler()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	// Create an API token directly in the DB.
	plaintext, _, err := database.CreateToken(context.Background(), "test", nil, "read,write")
	require.Nil(t, err)

	return &testEnv{ts: ts, database: database, token: plaintext, cfg: cfg, store: store, handler: handler}
}

// helpers -------------------------------------------------------------------

func (e *testEnv) authGet(t *testing.T, path string) *http.Response {
	t.Helper()
	return e.doRequest(t, "GET", path, "", nil, true)
}

func (e *testEnv) get(t *testing.T, path string) *http.Response {
	t.Helper()
	return e.doRequest(t, "GET", path, "", nil, false)
}

func (e *testEnv) postJSON(t *testing.T, path, body string) *http.Response {
	t.Helper()
	return e.doRequest(t, "POST", path, "application/json", strings.NewReader(body), true)
}

func (e *testEnv) putBody(t *testing.T, path string, body []byte) *http.Response {
	t.Helper()
	return e.doRequest(t, "PUT", path, "application/octet-stream", bytes.NewReader(body), true)
}

func (e *testEnv) doRequest(t *testing.T, method, path, contentType string, body io.Reader, auth bool) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, e.ts.URL+path, body)
	require.Nil(t, err)

	if e.cfg.PrimaryDomain != "" {
		req.Host = e.cfg.PrimaryDomain
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	require.Nil(t, err)

	return resp
}

func (e *testEnv) doSubdomainRequest(t *testing.T, method, subdomain, path, contentType string, body io.Reader, auth bool) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, e.ts.URL+path, body)
	require.Nil(t, err)

	req.Host = subdomain + ".test.local"

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	require.Nil(t, err)

	return resp
}

func (e *testEnv) authGetSubdomain(t *testing.T, subdomain, path string) *http.Response {
	t.Helper()
	return e.doSubdomainRequest(t, "GET", subdomain, path, "", nil, true)
}

func (e *testEnv) getSubdomain(t *testing.T, subdomain, path string) *http.Response {
	t.Helper()
	return e.doSubdomainRequest(t, "GET", subdomain, path, "", nil, false)
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	require.NoError(t, json.NewDecoder(resp.Body).Decode(v))

}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.Nil(t, err)

	return b
}

// ---------------------------------------------------------------------------
// Full lifecycle integration test
// ---------------------------------------------------------------------------

func TestFullLifecycle(t *testing.T) {
	t.Serial()
	env := setup(t)

	binaryPayload := []byte("#!/bin/sh\necho hello world\n")

	// (a) Create project
	resp := env.postJSON(t, "/api/v1/projects", `{"name":"myapp","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var project db.Project
	decodeJSON(t, resp, &project)
	require.Equal(t, "myapp", project.Name)

	require.Equal(t, db.VersioningAuto, project.Versioning)

	// (b) List projects
	resp = env.authGet(t, "/api/v1/projects")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var projects []db.Project
	decodeJSON(t, resp, &projects)
	found := false
	for _, p := range projects {
		if p.Name == "myapp" {
			found = true
		}
	}
	require.True(t, found)

	// (c) Create release
	resp = env.postJSON(t, "/api/v1/projects/myapp/releases", `{"git_branch":"master","git_commit":"abc123"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var release db.Release
	decodeJSON(t, resp, &release)
	require.Equal(t, "1", release.Version)

	require.Equal(t, int64(1), release.VersionNum)

	require.Equal(t, "master", release.GitBranch)

	require.Equal(t, "abc123", release.GitCommit)

	// (d) Upload artifact
	resp = env.putBody(t, "/api/v1/projects/myapp/releases/1/artifacts/linux/amd64", binaryPayload)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var artifact db.Artifact
	decodeJSON(t, resp, &artifact)
	require.Equal(t, db.OSLinux, artifact.OS)

	require.Equal(t, db.ArchAMD64, artifact.Arch)

	require.Equal(t, int64(len(binaryPayload)), artifact.Size)

	// (e) Publish release
	resp = env.postJSON(t, "/api/v1/projects/myapp/releases/1/publish", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var published db.Release
	decodeJSON(t, resp, &published)
	require.True(t, published.Published)

	// (f) Download raw binary by exact version -- redirects to static subdomain
	resp = env.getSubdomain(t, "dl", "/myapp?v=1&os=linux&arch=amd64")
	require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Location"), "static.test.local/file?")

	// (g) Download via "latest" alias -- redirects with resolved version
	resp = env.getSubdomain(t, "dl", "/myapp?os=linux&arch=amd64")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Location"), "static.test.local/file?")
	require.Contains(t, resp.Header.Get("Location"), "project=myapp")

	// (h) Download via branch -- redirects with resolved version
	resp = env.getSubdomain(t, "dl", "/myapp?branch=master&os=linux&arch=amd64")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Location"), "static.test.local/file?")

	// (i) Download tar.gz -- redirects with fmt=tar.gz
	resp = env.getSubdomain(t, "dl", "/myapp?v=1&os=linux&arch=amd64&fmt=tar.gz")
	require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Location"), "fmt=tar.gz")

	// (j) Download zip -- redirects with fmt=zip
	resp = env.getSubdomain(t, "dl", "/myapp?v=1&os=linux&arch=amd64&fmt=zip")
	require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Location"), "fmt=zip")

}

// ---------------------------------------------------------------------------
// Healthz
// ---------------------------------------------------------------------------

func TestHealthz(t *testing.T) {
	t.Serial()
	env := setup(t)
	resp := env.get(t, "/healthz")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body struct {
		Status  string `json:"status"`
		Commit  string `json:"commit"`
		Version string `json:"version"`
	}
	require.NoError(t, json.Unmarshal(readBody(t, resp), &body))
	require.Equal(t, "ok", body.Status)
	require.NotEmpty(t, body.Commit)  // "unknown" in tests, but never empty
	require.NotEmpty(t, body.Version) // "dev" in tests, but never empty
}

func TestHealthz_DBClosed(t *testing.T) {
	t.Serial()
	env := setup(t)

	// Close the database to simulate an unreachable DB.
	env.database.Close()

	resp := env.get(t, "/healthz")
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	var body struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Commit string `json:"commit"`
	}
	require.NoError(t, json.Unmarshal(readBody(t, resp), &body))
	require.Equal(t, "unhealthy", body.Status)
	require.Equal(t, "database unreachable", body.Error)
	require.NotEmpty(t, body.Commit)
}
