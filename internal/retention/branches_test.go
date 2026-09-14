package retention

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// branchAPI serves the GitHub branches endpoint over a fixed list, paginating
// exactly as GitHub does.
func branchAPI(t *testing.T, names []string, auth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth != nil {
			*auth = r.Header.Get("Authorization")
		}
		if r.URL.Path != "/repos/wow-look-at-my/proj/branches" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		page := 1
		fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)

		start := (page - 1) * githubBranchPageSize
		end := start + githubBranchPageSize
		if start > len(names) {
			start = len(names)
		}
		if end > len(names) {
			end = len(names)
		}
		out := make([]map[string]string, 0, end-start)
		for _, n := range names[start:end] {
			out = append(out, map[string]string{"name": n})
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(out))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGitHubBranchLister_ListsEveryBranch(t *testing.T) {
	t.Serial()
	srv := branchAPI(t, []string{"master", "v1", "feature/x"}, nil)

	lister := &GitHubBranchLister{BaseURL: srv.URL}
	live, err := lister.ListBranches(context.Background(), testRepo)
	require.NoError(t, err)

	assert.Equal(t, 3, live.Len())
	assert.True(t, live.ContainsAll("master", "v1", "feature/x"))
	assert.False(t, live.Contains("deleted"))
}

func TestGitHubBranchLister_Paginates(t *testing.T) {
	t.Serial()
	names := make([]string, 0, githubBranchPageSize*2+7)
	for i := range cap(names) {
		names = append(names, fmt.Sprintf("branch-%d", i))
	}
	srv := branchAPI(t, names, nil)

	lister := &GitHubBranchLister{BaseURL: srv.URL}
	live, err := lister.ListBranches(context.Background(), testRepo)
	require.NoError(t, err)

	assert.Equal(t, len(names), live.Len())
	assert.True(t, live.Contains(names[len(names)-1]), "the last page must be walked")
}

func TestGitHubBranchLister_SendsTheBearer(t *testing.T) {
	t.Serial()
	var seen string
	srv := branchAPI(t, []string{"master"}, &seen)

	lister := &GitHubBranchLister{
		BaseURL: srv.URL,
		Bearer:  func(context.Context, string, string) string { return "token-abc" },
	}
	_, err := lister.ListBranches(context.Background(), testRepo)
	require.NoError(t, err)
	assert.Equal(t, "Bearer token-abc", seen)
}

// Every failure below has to be an error rather than an empty set, because an
// empty set is indistinguishable from "every branch was deleted".
func TestGitHubBranchLister_FailsClosed(t *testing.T) {
	t.Serial()

	t.Run("non-200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		lister := &GitHubBranchLister{BaseURL: srv.URL}
		_, err := lister.ListBranches(context.Background(), testRepo)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "HTTP 404")
	})

	t.Run("empty branch list", func(t *testing.T) {
		srv := branchAPI(t, nil, nil)
		lister := &GitHubBranchLister{BaseURL: srv.URL}
		_, err := lister.ListBranches(context.Background(), testRepo)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reported no branches")
	})

	t.Run("unparseable body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "not json")
		}))
		defer srv.Close()

		lister := &GitHubBranchLister{BaseURL: srv.URL}
		_, err := lister.ListBranches(context.Background(), testRepo)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode branches")
	})

	t.Run("malformed repo path", func(t *testing.T) {
		lister := &GitHubBranchLister{BaseURL: "http://127.0.0.1:1"}
		for _, bad := range []string{"", "proj", "a/b/c"} {
			_, err := lister.ListBranches(context.Background(), bad)
			assert.Error(t, err, "repo path %q", bad)
		}
	})
}
