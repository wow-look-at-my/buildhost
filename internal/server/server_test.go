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

	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/server"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// testEnv bundles the objects needed by every integration test.
type testEnv struct {
	ts       *httptest.Server
	database *db.DB
	token    string // plaintext API token with read,write scopes
}

func setup(t *testing.T) *testEnv {
	t.Helper()

	dbDir := t.TempDir()
	storeDir := t.TempDir()

	dbPath := filepath.Join(dbDir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	store, err := storage.NewFilesystem(storeDir)
	if err != nil {
		t.Fatalf("storage.NewFilesystem: %v", err)
	}

	cfg := config.Config{
		ListenAddr: ":0",
		DataDir:    dbDir,
		DBPath:     dbPath,
		BaseURL:    "http://localhost",
	}

	srv := server.New(cfg, database, store)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Create an API token directly in the DB.
	plaintext, _, err := database.CreateToken(context.Background(), "test", nil, "read,write")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	return &testEnv{ts: ts, database: database, token: plaintext}
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
	if err != nil {
		t.Fatalf("NewRequest %s %s: %v", method, path, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do %s %s: %v", method, path, err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	return b
}

// ---------------------------------------------------------------------------
// Full lifecycle integration test
// ---------------------------------------------------------------------------

func TestFullLifecycle(t *testing.T) {
	env := setup(t)

	binaryPayload := []byte("#!/bin/sh\necho hello world\n")

	// (a) Create project
	resp := env.postJSON(t, "/api/v1/projects", `{"name":"myapp","versioning":"auto"}`)
	if resp.StatusCode != http.StatusCreated {
		body := readBody(t, resp)
		t.Fatalf("create project: status %d, body %s", resp.StatusCode, body)
	}
	var project model.Project
	decodeJSON(t, resp, &project)
	if project.Name != "myapp" {
		t.Fatalf("project name = %q, want %q", project.Name, "myapp")
	}
	if project.Versioning != model.VersioningAuto {
		t.Fatalf("project versioning = %q, want %q", project.Versioning, model.VersioningAuto)
	}

	// (b) List projects
	resp = env.authGet(t, "/api/v1/projects")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list projects: status %d", resp.StatusCode)
	}
	var projects []model.Project
	decodeJSON(t, resp, &projects)
	found := false
	for _, p := range projects {
		if p.Name == "myapp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("list projects: myapp not found in %v", projects)
	}

	// (c) Create release
	resp = env.postJSON(t, "/api/v1/projects/myapp/releases", `{"git_branch":"main","git_commit":"abc123"}`)
	if resp.StatusCode != http.StatusCreated {
		body := readBody(t, resp)
		t.Fatalf("create release: status %d, body %s", resp.StatusCode, body)
	}
	var release model.Release
	decodeJSON(t, resp, &release)
	if release.Version != "1" {
		t.Fatalf("release version = %q, want %q", release.Version, "1")
	}
	if release.VersionNum != 1 {
		t.Fatalf("release version_num = %d, want 1", release.VersionNum)
	}
	if release.GitBranch != "main" {
		t.Fatalf("release git_branch = %q, want %q", release.GitBranch, "main")
	}
	if release.GitCommit != "abc123" {
		t.Fatalf("release git_commit = %q, want %q", release.GitCommit, "abc123")
	}

	// (d) Upload artifact
	resp = env.putBody(t, "/api/v1/projects/myapp/releases/1/artifacts/linux/amd64", binaryPayload)
	if resp.StatusCode != http.StatusCreated {
		body := readBody(t, resp)
		t.Fatalf("upload artifact: status %d, body %s", resp.StatusCode, body)
	}
	var artifact model.Artifact
	decodeJSON(t, resp, &artifact)
	if artifact.OS != model.OSLinux {
		t.Fatalf("artifact os = %q, want %q", artifact.OS, model.OSLinux)
	}
	if artifact.Arch != model.ArchAMD64 {
		t.Fatalf("artifact arch = %q, want %q", artifact.Arch, model.ArchAMD64)
	}
	if artifact.Size != int64(len(binaryPayload)) {
		t.Fatalf("artifact size = %d, want %d", artifact.Size, len(binaryPayload))
	}

	// (e) Publish release
	resp = env.postJSON(t, "/api/v1/projects/myapp/releases/1/publish", `{}`)
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("publish release: status %d, body %s", resp.StatusCode, body)
	}
	var published model.Release
	decodeJSON(t, resp, &published)
	if !published.Published {
		t.Fatal("publish release: published = false, want true")
	}

	// (f) Download raw binary by exact version (no auth needed for public project)
	resp = env.get(t, "/dl/myapp/1/linux/amd64")
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("download exact version: status %d, body %s", resp.StatusCode, body)
	}
	dlBody := readBody(t, resp)
	if !bytes.Equal(dlBody, binaryPayload) {
		t.Fatalf("download exact version: got %d bytes, want %d", len(dlBody), len(binaryPayload))
	}

	// (g) Download via "latest" alias
	resp = env.get(t, "/dl/myapp/latest/linux/amd64")
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("download latest: status %d, body %s", resp.StatusCode, body)
	}
	dlBody = readBody(t, resp)
	if !bytes.Equal(dlBody, binaryPayload) {
		t.Fatalf("download latest: got %d bytes, want %d", len(dlBody), len(binaryPayload))
	}

	// (h) Download via branch
	resp = env.get(t, "/dl/myapp/branch/main/linux/amd64")
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("download branch: status %d, body %s", resp.StatusCode, body)
	}
	dlBody = readBody(t, resp)
	if !bytes.Equal(dlBody, binaryPayload) {
		t.Fatalf("download branch: got %d bytes, want %d", len(dlBody), len(binaryPayload))
	}

	// (i) Download tar.gz packaged version
	resp = env.get(t, "/dl/myapp/1/linux/amd64?format=tar.gz")
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("download tar.gz: status %d, body %s", resp.StatusCode, body)
	}
	targzBody := readBody(t, resp)
	if len(targzBody) == 0 {
		t.Fatal("download tar.gz: empty body")
	}

	// (j) Download zip packaged version
	resp = env.get(t, "/dl/myapp/1/linux/amd64?format=zip")
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("download zip: status %d, body %s", resp.StatusCode, body)
	}
	zipBody := readBody(t, resp)
	if len(zipBody) == 0 {
		t.Fatal("download zip: empty body")
	}
}

// ---------------------------------------------------------------------------
// Healthz
// ---------------------------------------------------------------------------

func TestHealthz(t *testing.T) {
	env := setup(t)
	resp := env.get(t, "/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz: status %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if string(body) != "ok" {
		t.Fatalf("healthz: body = %q, want %q", body, "ok")
	}
}

// ---------------------------------------------------------------------------
// Auth tests
// ---------------------------------------------------------------------------

func TestCreateProject_NoAuth_Returns401(t *testing.T) {
	env := setup(t)

	req, _ := http.NewRequest("POST", env.ts.URL+"/api/v1/projects", strings.NewReader(`{"name":"noauth","versioning":"auto"}`))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("create project without auth: status %d, want 401, body %s", resp.StatusCode, body)
	}
}

func TestPrivateProject_DownloadWithoutAuth_Returns401(t *testing.T) {
	env := setup(t)

	binaryPayload := []byte("secret-binary-data")

	// Create private project.
	resp := env.postJSON(t, "/api/v1/projects", `{"name":"secretapp","versioning":"auto","is_private":true}`)
	if resp.StatusCode != http.StatusCreated {
		body := readBody(t, resp)
		t.Fatalf("create private project: status %d, body %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// Create release.
	resp = env.postJSON(t, "/api/v1/projects/secretapp/releases", `{"git_branch":"main","git_commit":"def456"}`)
	if resp.StatusCode != http.StatusCreated {
		body := readBody(t, resp)
		t.Fatalf("create release: status %d, body %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// Upload artifact.
	resp = env.putBody(t, "/api/v1/projects/secretapp/releases/1/artifacts/linux/amd64", binaryPayload)
	if resp.StatusCode != http.StatusCreated {
		body := readBody(t, resp)
		t.Fatalf("upload artifact: status %d, body %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// Publish release.
	resp = env.postJSON(t, "/api/v1/projects/secretapp/releases/1/publish", `{}`)
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("publish: status %d, body %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// Attempt unauthenticated download -- expect 401.
	resp = env.get(t, "/dl/secretapp/1/linux/amd64")
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("download private without auth: status %d, want 401, body %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// Attempt unauthenticated latest download -- expect 401.
	resp = env.get(t, "/dl/secretapp/latest/linux/amd64")
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("download private latest without auth: status %d, want 401, body %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// Attempt unauthenticated branch download -- expect 401.
	resp = env.get(t, "/dl/secretapp/branch/main/linux/amd64")
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("download private branch without auth: status %d, want 401, body %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// With auth, download should succeed.
	resp = env.authGet(t, "/dl/secretapp/1/linux/amd64")
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("download private with auth: status %d, want 200, body %s", resp.StatusCode, body)
	}
	dlBody := readBody(t, resp)
	if !bytes.Equal(dlBody, binaryPayload) {
		t.Fatalf("download private with auth: content mismatch")
	}
}

// ---------------------------------------------------------------------------
// Additional edge cases
// ---------------------------------------------------------------------------

func TestDownload_NonexistentProject_Returns404(t *testing.T) {
	env := setup(t)

	resp := env.get(t, "/dl/nonexistent/1/linux/amd64")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("download nonexistent project: status %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCreateProject_Duplicate_Returns409(t *testing.T) {
	env := setup(t)

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"dupapp","versioning":"auto"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first create: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects", `{"name":"dupapp","versioning":"auto"}`)
	if resp.StatusCode != http.StatusConflict {
		body := readBody(t, resp)
		t.Fatalf("duplicate create: status %d, want 409, body %s", resp.StatusCode, body)
	}
	resp.Body.Close()
}

func TestUploadArtifact_NoAuth_Returns401(t *testing.T) {
	env := setup(t)

	// Create project and release first (with auth).
	resp := env.postJSON(t, "/api/v1/projects", `{"name":"authtest","versioning":"auto"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects/authtest/releases", `{"git_branch":"main","git_commit":"aaa"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create release: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Attempt upload without auth.
	req, _ := http.NewRequest("PUT", env.ts.URL+"/api/v1/projects/authtest/releases/1/artifacts/linux/amd64", bytes.NewReader([]byte("data")))
	req.Header.Set("Content-Type", "application/octet-stream")
	respNoAuth, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer respNoAuth.Body.Close()
	if respNoAuth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("upload without auth: status %d, want 401", respNoAuth.StatusCode)
	}
}

func TestPublishRelease_NoArtifacts_Returns400(t *testing.T) {
	env := setup(t)

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"emptyrel","versioning":"auto"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects/emptyrel/releases", `{"git_branch":"main","git_commit":"bbb"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create release: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Attempt to publish with no artifacts.
	resp = env.postJSON(t, "/api/v1/projects/emptyrel/releases/1/publish", `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		body := readBody(t, resp)
		t.Fatalf("publish no artifacts: status %d, want 400, body %s", resp.StatusCode, body)
	}
	resp.Body.Close()
}

func TestListProjects_HidesPrivateWithoutAuth(t *testing.T) {
	env := setup(t)

	// Create a public and a private project.
	resp := env.postJSON(t, "/api/v1/projects", `{"name":"pub","versioning":"auto"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create public: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects", `{"name":"priv","versioning":"auto","is_private":true}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create private: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Without auth, only the public project should appear.
	resp = env.get(t, "/api/v1/projects")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list projects: status %d", resp.StatusCode)
	}
	var projects []model.Project
	decodeJSON(t, resp, &projects)

	for _, p := range projects {
		if p.Name == "priv" {
			t.Fatal("list projects without auth: private project 'priv' should be hidden")
		}
	}

	// With auth, both projects should appear.
	resp = env.authGet(t, "/api/v1/projects")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list projects auth: status %d", resp.StatusCode)
	}
	decodeJSON(t, resp, &projects)

	foundPub := false
	foundPriv := false
	for _, p := range projects {
		if p.Name == "pub" {
			foundPub = true
		}
		if p.Name == "priv" {
			foundPriv = true
		}
	}
	if !foundPub || !foundPriv {
		t.Fatalf("list projects with auth: pub=%v priv=%v, want both true", foundPub, foundPriv)
	}
}

func TestAutoVersioning_IncrementsBeyondFirst(t *testing.T) {
	env := setup(t)

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"multiver","versioning":"auto"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Create first release.
	resp = env.postJSON(t, "/api/v1/projects/multiver/releases", `{"git_branch":"main","git_commit":"aaa"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create release 1: status %d", resp.StatusCode)
	}
	var rel1 model.Release
	decodeJSON(t, resp, &rel1)
	if rel1.Version != "1" {
		t.Fatalf("release 1 version = %q, want %q", rel1.Version, "1")
	}

	// Create second release.
	resp = env.postJSON(t, "/api/v1/projects/multiver/releases", `{"git_branch":"main","git_commit":"bbb"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create release 2: status %d", resp.StatusCode)
	}
	var rel2 model.Release
	decodeJSON(t, resp, &rel2)
	if rel2.Version != "2" {
		t.Fatalf("release 2 version = %q, want %q", rel2.Version, "2")
	}
	if rel2.VersionNum != 2 {
		t.Fatalf("release 2 version_num = %d, want 2", rel2.VersionNum)
	}
}
