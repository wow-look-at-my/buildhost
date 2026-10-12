package auth

// Tests for the dead-session re-auth flow: a bh_session cookie whose MAC still

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestCanAccessRepo_TokenDead401_CachedPerToken(t *testing.T) {
	t.Serial()
	var calls int
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") == "Bearer live" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized) // bad credentials
	}))
	defer gh.Close()
	orig := githubAPIBase
	githubAPIBase = gh.URL
	defer func() { githubAPIBase = orig }()

	g := NewGitHubAuth("cid", "secret")
	ctx := context.Background()

	allowed, tokenDead := g.canAccessRepo(ctx, "matt", "revoked", "PazerOP/UE553")
	assert.False(t, allowed)
	assert.True(t, tokenDead, "a 401 must classify as token-dead")

	// Authoritative for that token: served from cache, GitHub is not re-hit
	before := calls
	allowed, tokenDead = g.canAccessRepo(ctx, "matt", "revoked", "PazerOP/UE553")
	assert.False(t, allowed)
	assert.True(t, tokenDead)
	assert.Equal(t, before, calls, "the dead-token answer should be cached")

	// Re-auth mints a new token -> new fingerprint -> re-checked, not shadowed
	allowed, tokenDead = g.canAccessRepo(ctx, "matt", "live", "PazerOP/UE553")
	assert.True(t, allowed)
	assert.False(t, tokenDead)
}

func TestCanAccessRepo_TransientStatuses_NotTokenDead(t *testing.T) {
	t.Serial()
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var calls int
			gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
			}))
			defer gh.Close()
			orig := githubAPIBase
			githubAPIBase = gh.URL
			defer func() { githubAPIBase = orig }()

			g := NewGitHubAuth("cid", "secret")
			allowed, tokenDead := g.canAccessRepo(context.Background(), "matt", "tok", "PazerOP/UE553")
			assert.False(t, allowed)
			assert.False(t, tokenDead, "%d is transient, not token-dead", status)
			// Not cached: the next request must re-check GitHub.
			before := calls
			_, _ = g.canAccessRepo(context.Background(), "matt", "tok", "PazerOP/UE553")
			assert.Greater(t, calls, before, "a transient %d must not be cached", status)
		})
	}
}

// The regression this split exists for: a browser whose session cookie still
// MAC-verifies but whose embedded GitHub token has died (revoked, or an
// expiring token) used to be dead-ended on the misleading "your account doesn't
// have access" page for the cookie's remaining lifetime. Now the dead session
// is cleared and the browser is transparently sent back through sign-in,
// returning to the original URL.
func TestRequireProject_Browser_SessionTokenDead_ClearsSessionAndReauths(t *testing.T) {
	t.Serial()
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // GitHub: bad credentials
	}))
	defer gh.Close()
	orig := githubAPIBase
	githubAPIBase = gh.URL
	defer func() { githubAPIBase = orig }()

	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")

	proj := &db.Project{Name: "secret", IsPrivate: true, Versioning: "auto", GithubRepo: "PazerOP/secret"}
	require.NoError(t, d.CreateProject(context.Background(), proj))
	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "secret", access: ReadAccess}
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	handler := mw.Authenticate(requireProjectFunc(parse, inner))

	req := httptest.NewRequest("GET", "/secret/branch/pr-1/", nil)
	req.Host = "sites.pazer.build"
	req.Header.Set("Accept", "text/html")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: mintSession("bob", "dead-tok", time.Now().Add(time.Hour))})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	assert.True(t, strings.HasPrefix(loc, "https://pazer.build"+signinStartPath+"?next="), "got %q", loc)
	assert.Contains(t, loc, url.QueryEscape("https://sites.pazer.build/secret/branch/pr-1/"))

	// The dead session cookie is cleared, with the apex Domain it was set with
	var cleared *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			cleared = c
		}
	}
	require.NotNil(t, cleared, "the dead session cookie must be cleared")
	assert.Empty(t, cleared.Value)
	assert.Less(t, cleared.MaxAge, 0, "the clearing cookie must expire immediately")
	assert.Equal(t, "pazer.build", cleared.Domain)
}

func TestRequireProject_Browser_NoAccessAndTransient_NoReauth(t *testing.T) {
	t.Serial()
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer gh.Close()
			orig := githubAPIBase
			githubAPIBase = gh.URL
			defer func() { githubAPIBase = orig }()

			d := openTestDB(t)
			initTestMiddleware(t, d)
			mw.GitHub = NewGitHubAuth("cid", "secret")

			proj := &db.Project{Name: "secret", IsPrivate: true, Versioning: "auto", GithubRepo: "PazerOP/secret"}
			require.NoError(t, d.CreateProject(context.Background(), proj))
			parse := func(r *http.Request) RouteInfo {
				return testRouteInfo{project: "secret", access: ReadAccess}
			}
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("handler should not be called")
			})
			handler := mw.Authenticate(requireProjectFunc(parse, inner))

			req := httptest.NewRequest("GET", "/secret/branch/pr-1/", nil)
			req.Host = "sites.pazer.build"
			req.Header.Set("Accept", "text/html")
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: mintSession("bob", "tok", time.Now().Add(time.Hour))})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusForbidden, rec.Code)
			assert.Empty(t, rec.Header().Get("Location"), "a %d must not redirect", status)
			assert.Contains(t, rec.Body.String(), "Access denied")
			for _, c := range rec.Result().Cookies() {
				assert.NotEqual(t, sessionCookieName, c.Name, "a %d must not clear the session", status)
			}
		})
	}
}
