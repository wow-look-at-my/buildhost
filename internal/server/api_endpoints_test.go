package server_test

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// ---------------------------------------------------------------------------
// Auth tests
// ---------------------------------------------------------------------------

func TestCreateProject_NoAuth_Returns401(t *testing.T) {
	t.Serial()
	env := setup(t)

	req, _ := http.NewRequest("POST", env.ts.URL+"/api/v1/projects", strings.NewReader(`{"name":"noauth","versioning":"auto"}`))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header.
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	require.Nil(t, err)

	defer resp.Body.Close()

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

}

func TestPrivateProject_DownloadWithoutAuth_Returns401(t *testing.T) {
	t.Serial()
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

	resp = env.getSubdomain(t, "dl", "/secretapp?v=1&os=linux&arch=amd64")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp.Body.Close()

	resp = env.getSubdomain(t, "dl", "/secretapp?os=linux&arch=amd64")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp.Body.Close()

	resp = env.getSubdomain(t, "dl", "/secretapp?branch=main&os=linux&arch=amd64")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp.Body.Close()

	// Unauthenticated fetches where a FORMULA belongs must be clean HTTP
	for _, p := range []string{"/secretapp", "/Formula/secretapp.rb"} {
		resp = env.getSubdomain(t, "brew", p)
		require.Equalf(t, http.StatusUnauthorized, resp.StatusCode, "GET brew%s", p)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		errBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Truef(t, strings.HasPrefix(string(errBody), "{"), "error body must be JSON, got %q", errBody)
		resp.Body.Close()
	}

	// With auth, download redirects to the static subdomain. For a PRIVATE
	resp = env.authGetSubdomain(t, "dl", "/secretapp?v=1&os=linux&arch=amd64")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Location"), "static.test.local/file?")
	require.Contains(t, resp.Header.Get("Location"), "project=secretapp")
	require.Contains(t, resp.Header.Get("Location"), "token=bhdl_")
	require.Equal(t, "private, no-store", resp.Header.Get("Cache-Control"))

	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	resp.Body.Close()

	// Following the redirect Location anonymously must succeed: the signed
	resp = env.getSubdomain(t, "static", loc.Path+"?"+loc.RawQuery)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, binaryPayload, body)
}

// ---------------------------------------------------------------------------
// Additional edge cases
// ---------------------------------------------------------------------------

func TestDownload_NonexistentProject_Returns404(t *testing.T) {
	t.Serial()
	env := setup(t)

	resp := env.getSubdomain(t, "dl", "/nonexistent?v=1&os=linux&arch=amd64")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	resp.Body.Close()
}

func TestCreateProject_Duplicate_Returns409(t *testing.T) {
	t.Serial()
	env := setup(t)

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"dupapp","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects", `{"name":"dupapp","versioning":"auto"}`)
	require.Equal(t, http.StatusConflict, resp.StatusCode)

	resp.Body.Close()
}

func TestUploadArtifact_NoAuth_Returns401(t *testing.T) {
	t.Serial()
	env := setup(t)

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
	t.Serial()
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
	t.Serial()
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
	t.Serial()
	env := setup(t)

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"multiver","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects/multiver/releases", `{"git_branch":"main","git_commit":"aaa"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var rel1 db.Release
	decodeJSON(t, resp, &rel1)
	require.Equal(t, "1", rel1.Version)

	resp = env.postJSON(t, "/api/v1/projects/multiver/releases", `{"git_branch":"main","git_commit":"bbb"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var rel2 db.Release
	decodeJSON(t, resp, &rel2)
	require.Equal(t, "2", rel2.Version)

	require.Equal(t, int64(2), rel2.VersionNum)

}
