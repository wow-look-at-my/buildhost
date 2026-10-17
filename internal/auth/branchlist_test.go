package auth

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// branchPage answers one page of the branch list the way GitHub does: a full
// page means "ask again", a short one means "that was the last page".
func branchPage(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	assert.Equal(t, "buildhost", r.Header.Get("User-Agent"))
	assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
	assert.Equal(t, "100", r.URL.Query().Get("per_page"))

	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	require.NoError(t, err)

	switch page {
	case 1:
		fmt.Fprint(w, `[`)
		for i := 0; i < branchListPageSize; i++ {
			if i > 0 {
				fmt.Fprint(w, `,`)
			}
			fmt.Fprintf(w, `{"name":"bulk-%d"}`, i)
		}
		fmt.Fprint(w, `]`)
	case 2:
		fmt.Fprint(w, `[{"name":"master"},{"name":"v1"}]`)
	default:
		fmt.Fprint(w, `[]`)
	}
}

// A branch past the first page is still a live branch. Paginating is what keeps
// a busy repository's older branches from being read as deleted.
func TestLiveBranches_PaginatesPastTheFirstPage(t *testing.T) {
	t.Serial()
	var hits atomic.Int32
	withStubGitHub(t, "ghp_secret", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		assert.Equal(t, "/repos/acme/widget/branches", r.URL.Path)
		assert.Equal(t, "Bearer ghp_secret", r.Header.Get("Authorization"))
		branchPage(t, w, r)
	})

	names, err := LiveBranches(context.Background(), "acme/widget")
	require.NoError(t, err)

	assert.Contains(t, names, "master")
	assert.Contains(t, names, "v1", "a branch on the second page is live, not deleted")
	assert.Len(t, names, branchListPageSize+2)
	assert.Equal(t, int32(2), hits.Load(), "a full page must be followed by the next one")
}

// The seam the retention pass wires up must reach the same lookup.
func TestBranchLister_DelegatesToLiveBranches(t *testing.T) {
	t.Serial()
	withStubGitHub(t, "", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"name":"main"}]`)
	})

	names, err := BranchLister{}.LiveBranches(context.Background(), "acme/one-branch")
	require.NoError(t, err)
	assert.Equal(t, []string{"main"}, names)
}

// An error is an unknown answer, never an empty repository: the caller keeps
// every release of a repository it could not ask about.
func TestLiveBranches_ErrorsOnHTTPFailure(t *testing.T) {
	t.Serial()
	withStubGitHub(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
	})

	names, err := LiveBranches(context.Background(), "acme/throttled")
	require.Error(t, err)
	assert.Nil(t, names)
	assert.Contains(t, err.Error(), "acme/throttled")
	assert.Contains(t, err.Error(), "403")
}

// An empty branch list is a wrong answer for a repository buildhost published
// from, so it fails closed rather than marking every branch deleted at once.
func TestLiveBranches_ErrorsOnEmptyList(t *testing.T) {
	t.Serial()
	withStubGitHub(t, "", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[]`)
	})

	names, err := LiveBranches(context.Background(), "acme/empty")
	require.Error(t, err)
	assert.Nil(t, names)
	assert.Contains(t, err.Error(), "reported no branches")
}

func TestLiveBranches_RejectsBadRepoPath(t *testing.T) {
	t.Serial()
	var hits atomic.Int32
	withStubGitHub(t, "", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `[{"name":"main"}]`)
	})

	for _, bad := range []string{"", "noslash", "a/b/c", "owner/../etc", "owner/re po", "owner/"} {
		names, err := LiveBranches(context.Background(), bad)
		assert.Error(t, err, bad)
		assert.Nil(t, names, bad)
	}
	assert.Equal(t, int32(0), hits.Load(), "a malformed repo path must never reach GitHub")
}

func TestLiveBranches_ErrorsOnUnparseableBody(t *testing.T) {
	t.Serial()
	withStubGitHub(t, "", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"not":"a branch list"}`)
	})

	names, err := LiveBranches(context.Background(), "acme/garbage")
	require.Error(t, err)
	assert.Nil(t, names)
}

// One short page ends the walk.
func TestLiveBranches_OneShortPageStops(t *testing.T) {
	t.Serial()
	var hits atomic.Int32
	withStubGitHub(t, "", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `[{"name":"main"},{"name":"dev"}]`)
	})

	names, err := LiveBranches(context.Background(), "acme/two-branches")
	require.NoError(t, err)
	assert.Equal(t, []string{"main", "dev"}, names)
	assert.Equal(t, int32(1), hits.Load())
}
