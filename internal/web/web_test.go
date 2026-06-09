package web_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/server"
	"github.com/wow-look-at-my/buildhost/internal/storage"

	// Blank-importing the frontend is not needed (the package under test links
	// it), but no service backends are imported here on purpose: that keeps the
	// apt signing-key generation (its OnReady) out of the test, so setup is fast.
	_ "github.com/wow-look-at-my/buildhost/internal/web"
)

type env struct {
	ts    *httptest.Server
	token string
}

func setup(t *testing.T) *env {
	t.Helper()
	dbDir := t.TempDir()
	storeDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")

	database, err := db.Open(dbPath)
	require.Nil(t, err)
	t.Cleanup(func() { database.Close() })

	store, err := storage.NewFilesystem(storeDir, true)
	require.Nil(t, err)

	srv := server.New(config.Config{ListenAddr: ":0", DataDir: dbDir, DBPath: dbPath}, database, store)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	plaintext, _, err := database.CreateToken(context.Background(), "test", nil, "read,write")
	require.Nil(t, err)

	seed(t, database)
	return &env{ts: ts, token: plaintext}
}

// seed inserts a public project with one published release/artifact and a
// private project, directly via the DB (the frontend only reads metadata).
func seed(t *testing.T, database *db.DB) {
	t.Helper()
	ctx := context.Background()

	pub := &db.Project{Name: "myapp", Description: "A demo app", Versioning: db.VersioningAuto}
	require.Nil(t, database.CreateProject(ctx, pub))

	rel := &db.Release{ProjectID: pub.ID, Version: "1", VersionNum: 1, GitBranch: "main", GitCommit: "abcdef1234567890"}
	require.Nil(t, database.CreateRelease(ctx, rel))
	require.Nil(t, database.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64, Kind: db.KindBinary,
		StorageKey: strings.Repeat("a", 64), Size: 1048576, SHA256: strings.Repeat("b", 64), Filename: "myapp",
	}))
	require.Nil(t, database.PublishRelease(ctx, rel.ID))

	priv := &db.Project{Name: "secret", IsPrivate: true, Versioning: db.VersioningAuto}
	require.Nil(t, database.CreateProject(ctx, priv))
}

func (e *env) get(t *testing.T, path string, withAuth bool) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest("GET", e.ts.URL+path, nil)
	require.Nil(t, err)
	if withAuth {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	require.Nil(t, err)
	body, err := io.ReadAll(resp.Body)
	require.Nil(t, err)
	resp.Body.Close()
	return resp, string(body)
}

func TestFrontend(t *testing.T) {
	e := setup(t)

	t.Run("home lists public projects, hides private", func(t *testing.T) {
		resp, body := e.get(t, "/", false)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
		require.Contains(t, body, "myapp")
		require.NotContains(t, body, "secret")
	})

	t.Run("home is server-rendered with no script tags", func(t *testing.T) {
		_, body := e.get(t, "/", false)
		require.NotContains(t, strings.ToLower(body), "<script")
		require.Contains(t, body, "<!DOCTYPE html>")
		require.Contains(t, body, `<link rel="stylesheet" href="/_ui/style.css">`)
	})

	t.Run("home relaxes CSP for the stylesheet but allows no scripts", func(t *testing.T) {
		resp, _ := e.get(t, "/", false)
		csp := resp.Header.Get("Content-Security-Policy")
		require.Contains(t, csp, "style-src 'self'")
		require.NotContains(t, csp, "script-src")
		require.NotContains(t, csp, "'unsafe-inline'")
	})

	t.Run("stylesheet served as css", func(t *testing.T) {
		resp, body := e.get(t, "/_ui/style.css", false)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "text/css; charset=utf-8", resp.Header.Get("Content-Type"))
		require.Contains(t, body, "--accent")
		require.NotEmpty(t, resp.Header.Get("ETag"))
	})

	t.Run("project page shows releases and install commands", func(t *testing.T) {
		resp, body := e.get(t, "/projects/myapp", false)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Contains(t, body, "A demo app")
		require.Contains(t, body, "Releases")
		require.Contains(t, body, "main") // git branch
		require.Contains(t, body, "/projects/myapp/releases/1")
		require.Contains(t, body, "brew install") // install command present
		require.Contains(t, body, "docker pull oci.")
	})

	t.Run("release page lists artifacts with download links", func(t *testing.T) {
		resp, body := e.get(t, "/projects/myapp/releases/1", false)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Contains(t, body, "linux")
		require.Contains(t, body, "amd64")
		require.Contains(t, body, "1.0 MiB")
		// raw + repackaged formats, pointing at the dl subdomain.
		require.Contains(t, body, "fmt=tar.gz")
		require.Contains(t, body, "://dl.")
	})

	t.Run("release page resolves 'latest'", func(t *testing.T) {
		resp, body := e.get(t, "/projects/myapp/releases/latest", false)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Contains(t, body, "amd64")
	})

	t.Run("private project 404s for anonymous, no existence leak", func(t *testing.T) {
		resp, body := e.get(t, "/projects/secret", false)
		// 404 (not 401/403), and identical to an unknown project, so the
		// response never reveals that "secret" exists -- like GitHub.
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.NotContains(t, body, "secret")
	})

	t.Run("private project release page 404s for anonymous", func(t *testing.T) {
		resp, _ := e.get(t, "/projects/secret/releases/1", false)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("private project visible with authorized token", func(t *testing.T) {
		resp, body := e.get(t, "/projects/secret", true)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Contains(t, body, "secret")
		require.Contains(t, body, "private")
	})

	t.Run("authorized token sees private project on home", func(t *testing.T) {
		resp, body := e.get(t, "/", true)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Contains(t, body, "secret")
	})

	t.Run("unknown project 404", func(t *testing.T) {
		resp, _ := e.get(t, "/projects/nope", false)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("unknown release 404", func(t *testing.T) {
		resp, _ := e.get(t, "/projects/myapp/releases/999", false)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("unknown main-domain path 404", func(t *testing.T) {
		resp, _ := e.get(t, "/totally/unknown", false)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}
