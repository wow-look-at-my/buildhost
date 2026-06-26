package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestGitHubAuth_Disabled(t *testing.T) {
	assert.Nil(t, NewGitHubAuth("", "sec"))
	assert.Nil(t, NewGitHubAuth("id", ""))
	assert.NotNil(t, NewGitHubAuth("id", "sec"))
}

func TestSession_RoundTrip(t *testing.T) {
	v := mintSession("alice", "gho_tok", time.Now().Add(time.Hour))
	login, token, ok := verifySession(v)
	assert.True(t, ok)
	assert.Equal(t, "alice", login)
	assert.Equal(t, "gho_tok", token)

	_, _, ok = verifySession(v + "x")
	assert.False(t, ok)
	_, _, ok = verifySession("nope")
	assert.False(t, ok)
	_, _, ok = verifySession(mintSession("alice", "tok", time.Now().Add(-time.Minute)))
	assert.False(t, ok)
}

func TestState_RoundTrip(t *testing.T) {
	st := signState("nonce123", "https://sites.x.com/p/branch/b/?a=1", time.Now().Add(time.Minute))
	nonce, next, ok := verifyState(st)
	assert.True(t, ok)
	assert.Equal(t, "nonce123", nonce)
	assert.Equal(t, "https://sites.x.com/p/branch/b/?a=1", next)

	_, _, ok = verifyState(st + "x")
	assert.False(t, ok)
	_, _, ok = verifyState(signState("n", "/x", time.Now().Add(-time.Minute)))
	assert.False(t, ok)
}

func TestSafeNextURL(t *testing.T) {
	r := httptest.NewRequest("GET", "/__signin", nil)
	r.Host = "pazer.build"
	assert.Equal(t, "/p/branch/b/", safeNextURL(r, "/p/branch/b/"))
	assert.Equal(t, "https://sites.pazer.build/x", safeNextURL(r, "https://sites.pazer.build/x"))
	assert.Equal(t, "https://pazer.build/", safeNextURL(r, "https://evil.com/x"))
	assert.Equal(t, "https://pazer.build/", safeNextURL(r, "//evil.com"))
	assert.Equal(t, "https://pazer.build/", safeNextURL(r, ""))
	assert.Equal(t, "https://dl.pazer.build/x", safeNextURL(r, "https://dl.pazer.build/x"))
}

func TestSigninStart_RedirectsToGitHub(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("client-abc", "secret")

	req := httptest.NewRequest("GET", signinStartPath+"?next=%2Fsecret%2Fbranch%2Fpr-190%2F", nil)
	req.Host = "pazer.build"
	rec := httptest.NewRecorder()
	handleSigninStart(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	u, err := url.Parse(rec.Header().Get("Location"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(u.String(), githubAuthorizeURL+"?"))
	assert.Equal(t, "client-abc", u.Query().Get("client_id"))
	assert.Equal(t, "repo", u.Query().Get("scope"))
	assert.Equal(t, "https://pazer.build/__signin/callback", u.Query().Get("redirect_uri"))
	assert.NotEmpty(t, u.Query().Get("state"))
	assert.Contains(t, rec.Header().Get("Set-Cookie"), stateCookieName+"=")
}

func TestSigninStart_NotConfigured_501(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	req := httptest.NewRequest("GET", signinStartPath, nil)
	rec := httptest.NewRecorder()
	handleSigninStart(rec, req)
	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestSigninCallback_ValidLogin_SetsSession(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			w.Write([]byte(`{"access_token":"gho_test"}`))
		case r.URL.Path == "/user":
			w.Write([]byte(`{"login":"alice"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer gh.Close()
	origToken, origAPI := githubTokenURL, githubAPIBase
	githubTokenURL, githubAPIBase = gh.URL, gh.URL
	defer func() { githubTokenURL, githubAPIBase = origToken, origAPI }()

	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")

	nonce := "nonce-xyz"
	next := "https://sites.pazer.build/secret/branch/pr-190/"
	state := signState(nonce, next, time.Now().Add(time.Minute))
	req := httptest.NewRequest("GET", signinCallbackPath+"?code=abc&state="+url.QueryEscape(state), nil)
	req.Host = "pazer.build"
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: nonce})
	rec := httptest.NewRecorder()
	handleSigninCallback(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, next, rec.Header().Get("Location"))

	var sc *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			sc = c
		}
	}
	require.NotNil(t, sc, "session cookie must be set")
	assert.Equal(t, "pazer.build", sc.Domain)
	login, token, ok := verifySession(sc.Value)
	assert.True(t, ok)
	assert.Equal(t, "alice", login)
	assert.Equal(t, "gho_test", token)
}

func TestSigninCallback_StateMismatch_Rejected(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")

	state := signState("real-nonce", "/x", time.Now().Add(time.Minute))
	req := httptest.NewRequest("GET", signinCallbackPath+"?code=abc&state="+url.QueryEscape(state), nil)
	req.Host = "pazer.build"
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: "different-nonce"})
	rec := httptest.NewRecorder()
	handleSigninCallback(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCanAccessRepo(t *testing.T) {
	var calls int
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/repos/PazerOP/allowed" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer gh.Close()
	orig := githubAPIBase
	githubAPIBase = gh.URL
	defer func() { githubAPIBase = orig }()

	g := NewGitHubAuth("cid", "secret")
	ctx := context.Background()
	assert.True(t, g.canAccessRepo(ctx, "alice", "tok", "PazerOP/allowed"))
	assert.False(t, g.canAccessRepo(ctx, "alice", "tok", "PazerOP/denied"))
	// Cached: a repeat does not hit GitHub again.
	before := calls
	assert.True(t, g.canAccessRepo(ctx, "alice", "tok", "PazerOP/allowed"))
	assert.Equal(t, before, calls, "second check should be served from cache")
	// Missing inputs => false, no call.
	assert.False(t, g.canAccessRepo(ctx, "", "tok", "PazerOP/allowed"))
	assert.False(t, g.canAccessRepo(ctx, "alice", "", "PazerOP/allowed"))
}

// A transient GitHub failure (5xx/429/network/rate-limit 403) must NOT be cached
// as a hard denial. Regression: a momentary blip on the first check after
// sign-in pinned an authorized repo owner to "Access denied" for the whole cache
// TTL, even though GitHub would have returned 200 on the very next call.
func TestCanAccessRepo_TransientFailureNotCached(t *testing.T) {
	status := http.StatusInternalServerError
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
	ctx := context.Background()

	// First check hits a transient 500 -> denied, but the non-answer is not cached.
	assert.False(t, g.canAccessRepo(ctx, "matt", "tok", "PazerOP/UE553"))
	// GitHub recovers; the next check must re-hit GitHub (not the cache) and now
	// succeed -- the owner is not locked out by the earlier blip.
	status = http.StatusOK
	before := calls
	assert.True(t, g.canAccessRepo(ctx, "matt", "tok", "PazerOP/UE553"),
		"a transient failure must not be cached as a hard denial")
	assert.Greater(t, calls, before, "recovery check must reach GitHub, not a cached deny")
}

// A user who re-signs-in with a fresh, broader-scoped token is not shadowed by a
// negative result cached against their previous token: the cache key includes a
// token fingerprint, so the new token is re-checked rather than inheriting the
// old token's authoritative 404.
func TestCanAccessRepo_NewTokenNotShadowedByStaleNegative(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer good" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound) // authoritative "no access" for the old token
	}))
	defer gh.Close()
	orig := githubAPIBase
	githubAPIBase = gh.URL
	defer func() { githubAPIBase = orig }()

	g := NewGitHubAuth("cid", "secret")
	ctx := context.Background()

	// Old, insufficient token: authoritative 404 -> denied (and cached for it).
	assert.False(t, g.canAccessRepo(ctx, "matt", "scopeless", "PazerOP/UE553"))
	// Re-auth yields a new token with access; it must be re-checked, not shadowed
	// by the cached deny keyed to the previous token.
	assert.True(t, g.canAccessRepo(ctx, "matt", "good", "PazerOP/UE553"),
		"a new token must be re-checked, not shadowed by the previous token's cached deny")
}

// A browser hitting a private resource with no session, when GitHub login is
// configured, is redirected to /__signin (off to GitHub) on the apex.
func TestRequireProject_Browser_GitHubEnabled_RedirectsToSignin(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")

	proj := &db.Project{Name: "secret", IsPrivate: true, Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))
	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "secret", access: ReadAccess}
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	handler := requireProjectFunc(parse, inner)

	req := httptest.NewRequest("GET", "/secret/branch/pr-190/", nil)
	req.Host = "sites.pazer.build"
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	assert.True(t, strings.HasPrefix(loc, "https://pazer.build"+signinStartPath+"?next="), "got %q", loc)
	assert.Contains(t, loc, url.QueryEscape("https://sites.pazer.build/secret/branch/pr-190/"))
}

// End-to-end through the middleware: a signed-in user WITH access to the
// project's repo is allowed; one WITHOUT access is denied -- repo access is the
// gate, no org allowlist.
func TestSessionCookie_RepoAccessGatesPrivateProject(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/PazerOP/allowed" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer gh.Close()
	orig := githubAPIBase
	githubAPIBase = gh.URL
	defer func() { githubAPIBase = orig }()

	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")

	allowed := &db.Project{Name: "allowed", IsPrivate: true, Versioning: "auto", GithubRepo: "PazerOP/allowed"}
	denied := &db.Project{Name: "denied", IsPrivate: true, Versioning: "auto", GithubRepo: "PazerOP/denied"}
	require.NoError(t, d.CreateProject(context.Background(), allowed))
	require.NoError(t, d.CreateProject(context.Background(), denied))

	run := func(projName string) int {
		parse := func(r *http.Request) RouteInfo {
			return testRouteInfo{project: projName, access: ReadAccess}
		}
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
		handler := mw.Authenticate(requireProjectFunc(parse, inner))
		req := httptest.NewRequest("GET", "/"+projName+"/branch/pr-1/", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: mintSession("alice", "tok", time.Now().Add(time.Hour))})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	assert.Equal(t, http.StatusOK, run("allowed"), "user with repo access is allowed")
	assert.Equal(t, http.StatusUnauthorized, run("denied"), "user without repo access is denied")
}

// A signed-in browser that lacks access to the project's repo gets an actionable
// HTML page (403) -- NOT a redirect (which would loop) and NOT the dead-end JSON
// 401 a browser cannot act on. The page names the repo and offers a sign-out.
func TestRequireProject_Browser_SignedInButForbidden_HTMLPage(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // user can't see any repo
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
	assert.Empty(t, rec.Header().Get("Location"), "must not redirect a signed-in user (would loop)")
	body := rec.Body.String()
	assert.Contains(t, body, "Access denied")
	assert.Contains(t, body, "bob")            // who you're signed in as
	assert.Contains(t, body, "PazerOP/secret") // the repo you need
	// Sign-out link points at the apex __signout with a next= back to the resource.
	assert.Contains(t, body, signoutPath)
	assert.Contains(t, body, url.QueryEscape("https://sites.pazer.build/secret/branch/pr-1/"))
	assert.NotContains(t, body, "authentication required")
}

// A project with no recorded GitHub repo cannot be opened via GitHub login.
func TestUserCanReadProject_NoRepo_Denied(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")

	proj := &db.Project{Name: "norepo", IsPrivate: true, Versioning: "auto"} // GithubRepo == ""
	ctx := WithGitHubToken(WithUser(context.Background(), "alice"), "tok")
	assert.False(t, userCanReadProject(ctx, proj))
}
