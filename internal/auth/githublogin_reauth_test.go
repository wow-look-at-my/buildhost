package auth

// Tests for the dead-session re-auth flow: a bh_session cookie whose MAC still
// verifies but whose embedded GitHub token has died (GitHub 401 on the
// repo-access probe) must clear the session and 303 the browser back through
// /__signin -- while a 404 (genuine no access) keeps the "Access denied" page
// and 403/429/5xx stay transient (no redirect, nothing cached).

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

// A GitHub 401 on the access probe means the session's embedded token itself is
// dead (revoked or expired) -- distinct from 404 (genuine no access) and from
// transient failures. It is authoritative for that token, so it is cached under
// the token's fingerprint; a fresh sign-in mints a new token and is re-checked.
func TestCanAccessRepo_TokenDead401_CachedPerToken(t *testing.T) {
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
	// while the browser follows the re-auth redirect.
	before := calls
	allowed, tokenDead = g.canAccessRepo(ctx, "matt", "revoked", "PazerOP/UE553")
	assert.False(t, allowed)
	assert.True(t, tokenDead)
	assert.Equal(t, before, calls, "the dead-token answer should be cached")

	// Re-auth mints a new token -> new fingerprint -> re-checked, not shadowed
	// by the dead predecessor.
	allowed, tokenDead = g.canAccessRepo(ctx, "matt", "live", "PazerOP/UE553")
	assert.True(t, allowed)
	assert.False(t, tokenDead)
}

// Rate limiting and outages surface as 403/429/5xx on this endpoint -- never
// 401 -- and must NOT classify as token-dead (which would kick a signed-in user
// through a pointless re-auth on every GitHub hiccup). They stay transient:
// denied for the one request, nothing cached.
func TestCanAccessRepo_TransientStatuses_NotTokenDead(t *testing.T) {
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

	// 303 back through sign-in, carrying a next= to the original resource.
	require.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	assert.True(t, strings.HasPrefix(loc, "https://pazer.build"+signinStartPath+"?next="), "got %q", loc)
	assert.Contains(t, loc, url.QueryEscape("https://sites.pazer.build/secret/branch/pr-1/"))

	// The dead session cookie is cleared, with the apex Domain it was set with
	// (the request arrived on a service subdomain) -- a mismatched Domain would
	// leave browsers holding the dead cookie.
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

// Only a 401 triggers the re-auth flow. A 404 (the account genuinely lacks
// access) keeps the actionable "Access denied" page, and 403/429/5xx (rate
// limit / outage -- transient) must neither redirect nor clear the session:
// mis-classifying those as token-dead would bounce a signed-in user through a
// pointless re-auth on every GitHub hiccup.
func TestRequireProject_Browser_NoAccessAndTransient_NoReauth(t *testing.T) {
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
