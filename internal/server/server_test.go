package server_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/wow-look-at-my/buildhost/internal/api"
	_ "github.com/wow-look-at-my/buildhost/internal/apt"
	_ "github.com/wow-look-at-my/buildhost/internal/brew"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	_ "github.com/wow-look-at-my/buildhost/internal/dl"
	_ "github.com/wow-look-at-my/buildhost/internal/npm"
	_ "github.com/wow-look-at-my/buildhost/internal/oci"
	"github.com/wow-look-at-my/buildhost/internal/server"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/testify/require"
)

// testEnv bundles the objects needed by every integration test.
type testEnv struct {
	ts		*httptest.Server
	database	*db.DB
	token		string	// plaintext API token with read,write scopes
}

func setup(t *testing.T) *testEnv {
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
		ListenAddr:	":0",
		DataDir:	dbDir,
		DBPath:		dbPath,
		BaseURL:	"http://localhost",
	}

	srv := server.New(cfg, database, store)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Create an API token directly in the DB.
	plaintext, _, err := database.CreateToken(context.Background(), "test", nil, "read,write")
	require.Nil(t, err)

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
	require.Nil(t, err)

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.Nil(t, err)

	return resp
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
	resp = env.postJSON(t, "/api/v1/projects/myapp/releases", `{"git_branch":"main","git_commit":"abc123"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var release db.Release
	decodeJSON(t, resp, &release)
	require.Equal(t, "1", release.Version)

	require.Equal(t, int64(1), release.VersionNum)

	require.Equal(t, "main", release.GitBranch)

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

	// (f) Download raw binary by exact version (no auth needed for public project)
	resp = env.get(t, "/dl/myapp/1/linux/amd64")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	dlBody := readBody(t, resp)
	require.True(t, bytes.Equal(dlBody, binaryPayload))

	// (g) Download via "latest" alias
	resp = env.get(t, "/dl/myapp/latest/linux/amd64")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	dlBody = readBody(t, resp)
	require.True(t, bytes.Equal(dlBody, binaryPayload))

	// (h) Download via branch
	resp = env.get(t, "/dl/myapp/branch/main/linux/amd64")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	dlBody = readBody(t, resp)
	require.True(t, bytes.Equal(dlBody, binaryPayload))

	// (i) Download tar.gz packaged version
	resp = env.get(t, "/dl/myapp/1/linux/amd64?format=tar.gz")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	targzBody := readBody(t, resp)
	require.NotEqual(t, 0, len(targzBody))

	// (j) Download zip packaged version
	resp = env.get(t, "/dl/myapp/1/linux/amd64?format=zip")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	zipBody := readBody(t, resp)
	require.NotEqual(t, 0, len(zipBody))

}

// ---------------------------------------------------------------------------
// Healthz
// ---------------------------------------------------------------------------

func TestHealthz(t *testing.T) {
	env := setup(t)
	resp := env.get(t, "/healthz")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body := readBody(t, resp)
	require.Equal(t, "ok", string(body))

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
	require.Nil(t, err)

	defer resp.Body.Close()

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

}

func TestPrivateProject_DownloadWithoutAuth_Returns401(t *testing.T) {
	env := setup(t)

	binaryPayload := []byte("secret-binary-data")

	// Create private project.
	resp := env.postJSON(t, "/api/v1/projects", `{"name":"secretapp","versioning":"auto","is_private":true}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp.Body.Close()

	// Create release.
	resp = env.postJSON(t, "/api/v1/projects/secretapp/releases", `{"git_branch":"main","git_commit":"def456"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp.Body.Close()

	// Upload artifact.
	resp = env.putBody(t, "/api/v1/projects/secretapp/releases/1/artifacts/linux/amd64", binaryPayload)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp.Body.Close()

	// Publish release.
	resp = env.postJSON(t, "/api/v1/projects/secretapp/releases/1/publish", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp.Body.Close()

	// Attempt unauthenticated download -- expect 401.
	resp = env.get(t, "/dl/secretapp/1/linux/amd64")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp.Body.Close()

	// Attempt unauthenticated latest download -- expect 401.
	resp = env.get(t, "/dl/secretapp/latest/linux/amd64")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp.Body.Close()

	// Attempt unauthenticated branch download -- expect 401.
	resp = env.get(t, "/dl/secretapp/branch/main/linux/amd64")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp.Body.Close()

	// With auth, download should succeed.
	resp = env.authGet(t, "/dl/secretapp/1/linux/amd64")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	dlBody := readBody(t, resp)
	require.True(t, bytes.Equal(dlBody, binaryPayload))

}

// ---------------------------------------------------------------------------
// Additional edge cases
// ---------------------------------------------------------------------------

func TestDownload_NonexistentProject_Returns404(t *testing.T) {
	env := setup(t)

	resp := env.get(t, "/dl/nonexistent/1/linux/amd64")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	resp.Body.Close()
}

func TestCreateProject_Duplicate_Returns409(t *testing.T) {
	env := setup(t)

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"dupapp","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects", `{"name":"dupapp","versioning":"auto"}`)
	require.Equal(t, http.StatusConflict, resp.StatusCode)

	resp.Body.Close()
}

func TestUploadArtifact_NoAuth_Returns401(t *testing.T) {
	env := setup(t)

	// Create project and release first (with auth).
	resp := env.postJSON(t, "/api/v1/projects", `{"name":"authtest","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects/authtest/releases", `{"git_branch":"main","git_commit":"aaa"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp.Body.Close()

	// Attempt upload without auth.
	req, _ := http.NewRequest("PUT", env.ts.URL+"/api/v1/projects/authtest/releases/1/artifacts/linux/amd64", bytes.NewReader([]byte("data")))
	req.Header.Set("Content-Type", "application/octet-stream")
	respNoAuth, err := http.DefaultClient.Do(req)
	require.Nil(t, err)

	defer respNoAuth.Body.Close()
	require.Equal(t, http.StatusUnauthorized, respNoAuth.StatusCode)

}

func TestPublishRelease_NoArtifacts_Returns400(t *testing.T) {
	env := setup(t)

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"emptyrel","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects/emptyrel/releases", `{"git_branch":"main","git_commit":"bbb"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp.Body.Close()

	// Attempt to publish with no artifacts.
	resp = env.postJSON(t, "/api/v1/projects/emptyrel/releases/1/publish", `{}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	resp.Body.Close()
}

func TestListProjects_HidesPrivateWithoutAuth(t *testing.T) {
	env := setup(t)

	// Create a public and a private project.
	resp := env.postJSON(t, "/api/v1/projects", `{"name":"pub","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects", `{"name":"priv","versioning":"auto","is_private":true}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp.Body.Close()

	// Without auth, only the public project should appear.
	resp = env.get(t, "/api/v1/projects")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var projects []db.Project
	decodeJSON(t, resp, &projects)

	for _, p := range projects {
		require.NotEqual(t, "priv", p.Name)

	}

	// With auth, both projects should appear.
	resp = env.authGet(t, "/api/v1/projects")
	require.Equal(t, http.StatusOK, resp.StatusCode)

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
	require.False(t, !foundPub || !foundPriv)

}

func TestAutoVersioning_IncrementsBeyondFirst(t *testing.T) {
	env := setup(t)

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"multiver","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp.Body.Close()

	// Create first release.
	resp = env.postJSON(t, "/api/v1/projects/multiver/releases", `{"git_branch":"main","git_commit":"aaa"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var rel1 db.Release
	decodeJSON(t, resp, &rel1)
	require.Equal(t, "1", rel1.Version)

	// Create second release.
	resp = env.postJSON(t, "/api/v1/projects/multiver/releases", `{"git_branch":"main","git_commit":"bbb"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var rel2 db.Release
	decodeJSON(t, resp, &rel2)
	require.Equal(t, "2", rel2.Version)

	require.Equal(t, int64(2), rel2.VersionNum)

}

func signJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid})
	payload, _ := json.Marshal(claims)
	content := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	hash := sha256.Sum256([]byte(content))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	require.NoError(t, err)
	return content + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func jwksServer(t *testing.T, pub *rsa.PublicKey, kid string) *httptest.Server {
	t.Helper()
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1})
	jwksBody := fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":"%s","n":"%s","e":"%s"}]}`, kid, n, e)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/.well-known/openid-configuration" {
			fmt.Fprintf(w, `{"jwks_uri":"%s/.well-known/jwks"}`, srv.URL)
			return
		}
		w.Write([]byte(jwksBody))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOIDC_AutoCreateProject(t *testing.T) {
	dbDir := t.TempDir()
	storeDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")
	database, err := db.Open(dbPath)
	require.Nil(t, err)
	t.Cleanup(func() { database.Close() })

	store, err := storage.NewFilesystem(storeDir, true)
	require.Nil(t, err)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	jwksSrv := jwksServer(t, &key.PublicKey, "kid-auto")

	cfg := config.Config{
		ListenAddr:  ":0",
		DataDir:     dbDir,
		DBPath:      dbPath,
		BaseURL:     "http://localhost",
		OIDCIssuers: []string{jwksSrv.URL},
		OIDCOrgs:    []string{"*"},
		OIDCEvents:  []string{"push"},
	}

	srv := server.New(cfg, database, store)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	token := signJWT(t, key, "kid-auto", map[string]any{
		"iss":        jwksSrv.URL,
		"sub":        "repo:myorg/autoproject:ref:refs/heads/main",
		"event_name": "push",
		"aud": ts.URL,
		"exp": time.Now().Add(10 * time.Minute).Unix(),
		"iat": time.Now().Unix(),
	})

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/projects/autoproject/releases", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusCreated, resp.StatusCode)

	proj, err := database.GetProject(context.Background(), "autoproject")
	require.NoError(t, err)
	require.Equal(t, "autoproject", proj.Name)
}
