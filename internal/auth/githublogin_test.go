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
	assert.Nil(t, NewGitHubAuth("", "sec", []string{"org"}))
	assert.Nil(t, NewGitHubAuth("id", "", []string{"org"}))
	assert.Nil(t, NewGitHubAuth("id", "sec", nil))
	assert.Nil(t, NewGitHubAuth("id", "sec", []string{"  "})) // blank-only orgs
	assert.NotNil(t, NewGitHubAuth("id", "sec", []string{"org"}))
}

func TestSession_RoundTrip(t *testing.T) {
	v := mintSession("alice", time.Now().Add(time.Hour))
	login, ok := verifySession(v)
	assert.True(t, ok)
	assert.Equal(t, "alice", login)

	_, ok = verifySession(v + "x")
	assert.False(t, ok)
	_, ok = verifySession("nope")
	assert.False(t, ok)
	_, ok = verifySession(mintSession("alice", time.Now().Add(-time.Minute)))
	assert.False(t, ok)
}

func TestState_RoundTrip(t *testing.T) {
	// next deliberately contains a NUL-free URL with query; the nonce/next split
	// must survive and the exp suffix must not be confused with next.
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
	r.Host = "pazer.build" // apex
	assert.Equal(t, "/p/branch/b/", safeNextURL(r, "/p/branch/b/"))
	assert.Equal(t, "https://sites.pazer.build/x", safeNextURL(r, "https://sites.pazer.build/x"))
	assert.Equal(t, "https://pazer.build/", safeNextURL(r, "https://evil.com/x")) // foreign host
	assert.Equal(t, "https://pazer.build/", safeNextURL(r, "//evil.com"))         // scheme-relative
	assert.Equal(t, "https://pazer.build/", safeNextURL(r, ""))                   // empty
	assert.Equal(t, "https://dl.pazer.build/x", safeNextURL(r, "https://dl.pazer.build/x"))
}

func TestSigninStart_RedirectsToGitHub(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("client-abc", "secret", []string{"PazerOP"})

	req := httptest.NewRequest("GET", signinStartPath+"?next=%2Fsecret%2Fbranch%2Fpr-190%2F", nil)
	req.Host = "pazer.build"
	rec := httptest.NewRecorder()
	handleSigninStart(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	assert.True(t, strings.HasPrefix(loc, githubAuthorizeURL+"?"), "got %q", loc)
	u, err := url.Parse(loc)
	require.NoError(t, err)
	assert.Equal(t, "client-abc", u.Query().Get("client_id"))
	assert.Equal(t, "read:org", u.Query().Get("scope"))
	assert.Equal(t, "https://pazer.build/__signin/callback", u.Query().Get("redirect_uri"))
	assert.NotEmpty(t, u.Query().Get("state"))
	assert.Contains(t, rec.Header().Get("Set-Cookie"), stateCookieName+"=")
}

func TestSigninStart_NotConfigured_501(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d) // mw.GitHub nil
	req := httptest.NewRequest("GET", signinStartPath, nil)
	rec := httptest.NewRecorder()
	handleSigninStart(rec, req)
	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

// Full callback against a mocked GitHub (token exchange + user + org membership).
func TestSigninCallback_AuthorizedMember_SetsSessionRedirects(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST": // token exchange
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"gho_test"}`))
		case r.URL.Path == "/user":
			w.Write([]byte(`{"login":"alice"}`))
		case r.URL.Path == "/user/memberships/orgs/PazerOP":
			w.Write([]byte(`{"state":"active"}`))
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
	mw.GitHub = NewGitHubAuth("cid", "secret", []string{"PazerOP"})

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
	assert.Equal(t, "pazer.build", sc.Domain) // domain-wide so subdomains see it
	login, ok := verifySession(sc.Value)
	assert.True(t, ok)
	assert.Equal(t, "alice", login)
}

func TestSigninCallback_NotAMember_Forbidden(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			w.Write([]byte(`{"access_token":"gho_test"}`))
		case r.URL.Path == "/user":
			w.Write([]byte(`{"login":"mallory"}`))
		default:
			w.WriteHeader(http.StatusNotFound) // not a member of any org
		}
	}))
	defer gh.Close()
	origToken, origAPI := githubTokenURL, githubAPIBase
	githubTokenURL, githubAPIBase = gh.URL, gh.URL
	defer func() { githubTokenURL, githubAPIBase = origToken, origAPI }()

	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret", []string{"PazerOP"})

	nonce := "n2"
	state := signState(nonce, "/secret/branch/pr-190/", time.Now().Add(time.Minute))
	req := httptest.NewRequest("GET", signinCallbackPath+"?code=abc&state="+url.QueryEscape(state), nil)
	req.Host = "pazer.build"
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: nonce})
	rec := httptest.NewRecorder()
	handleSigninCallback(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	for _, c := range rec.Result().Cookies() {
		assert.NotEqual(t, sessionCookieName, c.Name, "no session cookie when unauthorized")
	}
}

func TestSigninCallback_StateMismatch_Rejected(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret", []string{"PazerOP"})

	state := signState("real-nonce", "/x", time.Now().Add(time.Minute))
	req := httptest.NewRequest("GET", signinCallbackPath+"?code=abc&state="+url.QueryEscape(state), nil)
	req.Host = "pazer.build"
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: "different-nonce"}) // CSRF: cookie != state
	rec := httptest.NewRecorder()
	handleSigninCallback(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// A browser hitting a private resource with no session, when GitHub login is
// configured, is redirected to /__signin (off to GitHub) on the apex.
func TestRequireProject_PrivateProject_Browser_GitHubEnabled_RedirectsToSignin(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret", []string{"PazerOP"})

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
	// Redirect to the apex sign-in entrypoint, carrying the full original URL.
	assert.True(t, strings.HasPrefix(loc, "https://pazer.build"+signinStartPath+"?next="), "got %q", loc)
	assert.Contains(t, loc, url.QueryEscape("https://sites.pazer.build/secret/branch/pr-190/"))
}

// A request carrying a verified GitHub session may read a private resource with
// no project token; exercised through the real Authenticate->requireProject chain.
func TestSessionCookie_AuthenticatesThroughMiddleware(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret", []string{"PazerOP"})

	proj := &db.Project{Name: "secret", IsPrivate: true, Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))
	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "secret", access: ReadAccess}
	}
	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := mw.Authenticate(requireProjectFunc(parse, inner))

	req := httptest.NewRequest("GET", "/secret/branch/pr-190/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: mintSession("alice", time.Now().Add(time.Hour))})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.True(t, called, "valid session should authenticate through the middleware")
	assert.Equal(t, http.StatusOK, rec.Code)

	// Tampered session does not authenticate.
	called = false
	req2 := httptest.NewRequest("GET", "/secret/branch/pr-190/", nil)
	req2.AddCookie(&http.Cookie{Name: sessionCookieName, Value: mintSession("alice", time.Now().Add(time.Hour)) + "x"})
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	assert.False(t, called)
	assert.Equal(t, http.StatusUnauthorized, rec2.Code)
}
