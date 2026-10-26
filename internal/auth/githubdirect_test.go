// Tests point the direct GitHub fallback at the same fake as the mirror.

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setDirectAPI points the direct fallback at u and returns the restore func.
func setDirectAPI(u string) func() {
	orig := githubDirectAPIBase
	githubDirectAPIBase = u
	return func() { githubDirectAPIBase = orig }
}

func fakeGitHubUser(t *testing.T) *httptest.Server {
	t.Helper()
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			w.Write([]byte(`{"login":"matt","id":1}`))
		case "/repos/PazerOP/UE553":
			w.Write([]byte(`{"id":2}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(gh.Close)
	return gh
}

// Sign-in works when buildhost cannot reach the mirror at all.
func TestUserTokenGet_MirrorUnreachable_UsesGitHub(t *testing.T) {
	t.Serial()
	gh := fakeGitHubUser(t)
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	orig := githubAPIBase
	githubAPIBase = deadURL
	defer func() { githubAPIBase = orig }()
	defer setDirectAPI(gh.URL)()

	g := NewGitHubAuth("cid", "secret")
	login, err := g.fetchLogin(context.Background(), "ghu_test")
	require.NoError(t, err)
	assert.Equal(t, "matt", login)
	allowed, tokenDead := g.canAccessRepo(context.Background(), "matt", "ghu_test", "PazerOP/UE553")
	assert.True(t, allowed)
	assert.False(t, tokenDead)
}

// A mirror that refuses a token GitHub accepts does not block sign-in.
func TestUserTokenGet_MirrorRefuses_UsesGitHub(t *testing.T) {
	t.Serial()
	gh := fakeGitHubUser(t)
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized: could not validate GitHub credential", http.StatusUnauthorized)
	}))
	defer mirror.Close()
	orig := githubAPIBase
	githubAPIBase = mirror.URL
	defer func() { githubAPIBase = orig }()
	defer setDirectAPI(gh.URL)()

	login, err := NewGitHubAuth("cid", "secret").fetchLogin(context.Background(), "ghu_test")
	require.NoError(t, err)
	assert.Equal(t, "matt", login)
}

func TestUserTokenGet_Mirror404_IsFinal(t *testing.T) {
	t.Serial()
	directCalls := 0
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directCalls++
		w.Write([]byte(`{"id":2}`))
	}))
	defer direct.Close()
	mirror := httptest.NewServer(http.HandlerFunc(http.NotFound))
	defer mirror.Close()
	orig := githubAPIBase
	githubAPIBase = mirror.URL
	defer func() { githubAPIBase = orig }()
	defer setDirectAPI(direct.URL)()

	allowed, _ := NewGitHubAuth("cid", "secret").canAccessRepo(context.Background(), "matt", "ghu_test", "PazerOP/UE553")
	assert.False(t, allowed)
	assert.Equal(t, 0, directCalls)
}
