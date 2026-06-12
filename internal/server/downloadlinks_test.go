package server_test

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
)

// TestPrivateProject_TemporaryDownloadLink exercises the whole signed-link path
// end to end against a real server: a private artifact is unreachable without a
// credential, served by a valid signed &token= (no redirect, private cache), and
// rejected for a tampered / wrong-artifact / expired token. The server and this
// test share the same signing key because server.New -> auth.Init loaded it from
// the data dir, and auth.MintDownloadToken uses that same process-global key.
func TestPrivateProject_TemporaryDownloadLink(t *testing.T) {
	env := setup(t)
	payload := []byte("secret-binary-data-xyz")

	require.Equal(t, http.StatusCreated, env.postJSON(t, "/api/v1/projects", `{"name":"secretapp","versioning":"auto","is_private":true}`).StatusCode)
	require.Equal(t, http.StatusCreated, env.postJSON(t, "/api/v1/projects/secretapp/releases", `{"git_branch":"main","git_commit":"def456"}`).StatusCode)
	require.Equal(t, http.StatusCreated, env.putBody(t, "/api/v1/projects/secretapp/releases/1/artifacts/linux/amd64", payload).StatusCode)
	require.Equal(t, http.StatusOK, env.postJSON(t, "/api/v1/projects/secretapp/releases/1/publish", `{}`).StatusCode)

	// Canonical (sorted) query so the static handler serves directly without a
	// canonicalization redirect; token sorts between project and v.
	path := func(tok string) string {
		q := "/file?arch=amd64&fmt=raw&os=linux&project=secretapp"
		if tok != "" {
			q += "&token=" + tok
		}
		return q + "&v=1"
	}

	// No token: private artifact stays gated.
	resp := env.getSubdomain(t, "static", path(""))
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()

	// Valid signed token for exactly this artifact: served as-is, marked private.
	tok := auth.MintDownloadToken("secretapp", "1", "linux", "amd64", "raw", false, time.Now().Add(time.Hour))
	resp = env.getSubdomain(t, "static", path(tok))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, payload, body)
	require.Contains(t, resp.Header.Get("Cache-Control"), "private")

	// Tampered token: rejected.
	resp = env.getSubdomain(t, "static", path(tok+"x"))
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()

	// Token bound to a different artifact (darwin) does not unlock the linux one.
	wrongTok := auth.MintDownloadToken("secretapp", "1", "darwin", "amd64", "raw", false, time.Now().Add(time.Hour))
	resp = env.getSubdomain(t, "static", path(wrongTok))
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()

	// Expired token: rejected.
	expiredTok := auth.MintDownloadToken("secretapp", "1", "linux", "amd64", "raw", false, time.Now().Add(-time.Minute))
	resp = env.getSubdomain(t, "static", path(expiredTok))
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}
