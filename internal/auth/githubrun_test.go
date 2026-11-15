package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// With no credential the run check must fail before it sends a request, and name what is missing.
func TestGitHubRunEndedWithoutCredential(t *testing.T) {
	t.Serial("swaps the package-wide GitHub API base, token and App")
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "unauthorized: missing Authorization header", http.StatusUnauthorized)
	}))
	defer srv.Close()
	prevBase := gitHubAPIBase
	gitHubAPIBase = srv.URL
	SetGitHubToken("")
	require.NoError(t, SetGitHubApp("", ""))
	t.Cleanup(func() { gitHubAPIBase = prevBase })

	_, _, err := GitHubRunEnded(context.Background(), "org/repo", "42")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "buildhost has no GitHub credential")
	assert.Zero(t, calls.Load(), "an unauthenticated request must not go out")
}
