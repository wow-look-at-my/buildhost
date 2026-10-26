package auth

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func refStub(t *testing.T, found map[string]string) {
	t.Helper()
	withStubGitHub(t, "", func(w http.ResponseWriter, r *http.Request) {
		body, ok := found[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	})
}

func TestGitHubRefPresent_Branch(t *testing.T) {
	t.Serial()
	refStub(t, map[string]string{"/repos/o/r/branches/feature/x": `{}`})
	present, err := GitHubRefPresent(context.Background(), "o/r", "feature/x")
	require.NoError(t, err)
	assert.True(t, present)
}

func TestGitHubRefPresent_Tag(t *testing.T) {
	t.Serial()
	refStub(t, map[string]string{"/repos/o/r/git/ref/tags/v1.2.3": `{}`})
	present, err := GitHubRefPresent(context.Background(), "o/r", "v1.2.3")
	require.NoError(t, err)
	assert.True(t, present, "a tag build must not read as a deleted branch")
}

func TestGitHubRefPresent_Gone(t *testing.T) {
	t.Serial()
	refStub(t, nil)
	present, err := GitHubRefPresent(context.Background(), "o/r", "feature/x")
	require.NoError(t, err)
	assert.False(t, present)
}

func TestGitHubRefPresent_PullRequest(t *testing.T) {
	t.Serial()
	refStub(t, map[string]string{
		"/repos/o/r/pulls/7": `{"state":"open"}`,
		"/repos/o/r/pulls/8": `{"state":"closed"}`,
	})
	open, err := GitHubRefPresent(context.Background(), "o/r", "7/merge")
	require.NoError(t, err)
	assert.True(t, open, "an open pull request keeps its builds")

	closed, err := GitHubRefPresent(context.Background(), "o/r", "8/merge")
	require.NoError(t, err)
	assert.False(t, closed)

	missing, err := GitHubRefPresent(context.Background(), "o/r", "9/merge")
	require.NoError(t, err)
	assert.False(t, missing)
}

func TestGitHubRefPresent_ErrorIsUnknown(t *testing.T) {
	t.Serial()
	withStubGitHub(t, "", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	})
	_, err := GitHubRefPresent(context.Background(), "o/r", "feature/x")
	assert.Error(t, err, "a 403 must never read as a deleted branch")
}

func TestGitHubRepoDefaultBranch(t *testing.T) {
	t.Serial()
	refStub(t, map[string]string{"/repos/o/r": `{"default_branch":"trunk"}`})
	got, err := GitHubRepoDefaultBranch(context.Background(), "o/r")
	require.NoError(t, err)
	assert.Equal(t, "trunk", got)

	_, err = GitHubRepoDefaultBranch(context.Background(), "o/missing")
	assert.Error(t, err, "a repository GitHub does not answer for confirms nothing")
}
