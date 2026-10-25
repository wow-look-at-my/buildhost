package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

func stubVerify(t *testing.T, fn func(ctx context.Context, token string) (string, error)) {
	t.Helper()
	prev := verifyGitHubToken
	verifyGitHubToken = fn
	t.Cleanup(func() {
		verifyGitHubToken = prev
		auth.SetGitHubToken("")
	})
}

func TestGitHubCredential_SetUseAndRemove(t *testing.T) {
	t.Serial()
	srv, _ := newTestServer(t)
	stubVerify(t, func(_ context.Context, token string) (string, error) {
		require.Equal(t, "ghp_secret", token)
		return "octocat", nil
	})

	rec := serve(srv, "PUT", "/api/github-credential", bytes.NewBufferString(`{"token":" ghp_secret "}`))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "octocat")
	assert.Equal(t, "token", auth.GitHubCredentialSource(), "a saved token is in use at once")

	rec = serve(srv, "GET", "/api/github-credential", nil)
	require.Equal(t, 200, rec.Code)
	assert.NotContains(t, rec.Body.String(), "ghp_secret", "the token never goes back to the browser")
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, true, got["dashboard_set"])
	assert.Equal(t, auth.GitHubAPI, got["github_api"])

	rec = serve(srv, "DELETE", "/api/github-credential", nil)
	require.Equal(t, 200, rec.Code)
	assert.Equal(t, "none", auth.GitHubCredentialSource())
	rec = serve(srv, "GET", "/api/github-credential", nil)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, false, got["dashboard_set"])
}

func TestGitHubCredential_RefusedTokenIsNotSaved(t *testing.T) {
	t.Serial()
	srv, _ := newTestServer(t)
	stubVerify(t, func(context.Context, string) (string, error) {
		return "", errors.New("GitHub answered 401: Bad credentials")
	})

	rec := serve(srv, "PUT", "/api/github-credential", bytes.NewBufferString(`{"token":"bad"}`))
	assert.Equal(t, 400, rec.Code)
	assert.Contains(t, rec.Body.String(), "401")
	assert.Equal(t, "none", auth.GitHubCredentialSource())

	rec = serve(srv, "GET", "/api/github-credential", nil)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, false, got["dashboard_set"])
}
