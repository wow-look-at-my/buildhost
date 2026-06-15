package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

// withStubGitHub points the resolver at a stub server and a token for the test,
// restoring globals (and clearing the cache) afterward.
func withStubGitHub(t *testing.T, token string, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	prevBase, prevTok := gitHubAPIBase, currentGitHubToken()
	gitHubAPIBase = srv.URL
	SetGitHubToken(token)
	t.Cleanup(func() {
		gitHubAPIBase = prevBase
		SetGitHubToken(prevTok)
		branchCacheMu.Lock()
		branchCache = map[string]branchCacheEntry{}
		branchCacheMu.Unlock()
		srv.Close()
	})
	return srv
}

func TestGitHubDefaultBranch_Resolves(t *testing.T) {
	withStubGitHub(t, "", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/repos/wow-look-at-my/go-toolchain", r.URL.Path)
		assert.Equal(t, "buildhost", r.Header.Get("User-Agent"))
		fmt.Fprint(w, `{"default_branch":"v1","name":"go-toolchain"}`)
	})

	got := GitHubDefaultBranch(context.Background(), "wow-look-at-my/go-toolchain")
	assert.Equal(t, "v1", got)
}

func TestGitHubDefaultBranch_Caches(t *testing.T) {
	var hits atomic.Int32
	withStubGitHub(t, "", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `{"default_branch":"main"}`)
	})

	ctx := context.Background()
	assert.Equal(t, "main", GitHubDefaultBranch(ctx, "acme/widget"))
	assert.Equal(t, "main", GitHubDefaultBranch(ctx, "acme/widget"))
	assert.Equal(t, int32(1), hits.Load(), "second lookup must hit the cache, not GitHub")
}

func TestGitHubDefaultBranch_SendsToken(t *testing.T) {
	withStubGitHub(t, "ghp_secret", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer ghp_secret", r.Header.Get("Authorization"))
		fmt.Fprint(w, `{"default_branch":"trunk"}`)
	})

	assert.Equal(t, "trunk", GitHubDefaultBranch(context.Background(), "acme/tokened"))
}

func TestGitHubDefaultBranch_BestEffortOnError(t *testing.T) {
	// A rate-limited / unavailable GitHub yields "" (caller keeps the existing
	// default branch) rather than an error that fails the publish.
	withStubGitHub(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
	})

	assert.Equal(t, "", GitHubDefaultBranch(context.Background(), "acme/throttled"))
}

func TestGitHubDefaultBranch_RejectsBadRepoPath(t *testing.T) {
	var hits atomic.Int32
	withStubGitHub(t, "", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `{"default_branch":"main"}`)
	})

	for _, bad := range []string{"", "noslash", "a/b/c", "ok/../etc", "owner/re po", "owner/"} {
		assert.Equal(t, "", GitHubDefaultBranch(context.Background(), bad), bad)
	}
	assert.Equal(t, int32(0), hits.Load(), "a malformed repo path must never reach GitHub")
}

func TestRepoPathFromSubject(t *testing.T) {
	assert.Equal(t, "wow-look-at-my/go-toolchain",
		repoPathFromSubject("repo:wow-look-at-my/go-toolchain:ref:refs/heads/v1"))
	assert.Equal(t, "MyOrg/MyRepo", repoPathFromSubject("repo:MyOrg/MyRepo:pull_request"))
	assert.Equal(t, "", repoPathFromSubject("not-a-repo-subject"))
	assert.Equal(t, "", repoPathFromSubject("repo:noColonAfterRepo"))
}
