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
	v := signState(signinState{nonce: "nonce123", next: "https://sites.x.com/p/branch/b/?a=1"}, time.Now().Add(time.Minute))
	st, expired, ok := parseState(v)
	assert.True(t, ok)
	assert.False(t, expired)
	assert.Equal(t, "nonce123", st.nonce)
	assert.Equal(t, "https://sites.x.com/p/branch/b/?a=1", st.next)
	assert.False(t, st.retried)

	// The retried marker survives the round trip.
	st, expired, ok = parseState(signState(signinState{nonce: "n", next: "/x", retried: true}, time.Now().Add(time.Minute)))
	assert.True(t, ok)
	assert.False(t, expired)
	assert.True(t, st.retried)

	// Tampered: nothing is trusted.
	_, _, ok = parseState(v + "x")
	assert.False(t, ok)

	// Expired: authentic (payload still trusted) but flagged, so the callback
	// can restart the flow to the state's own next URL.
	st, expired, ok = parseState(signState(signinState{nonce: "n", next: "/x"}, time.Now().Add(-time.Minute)))
	assert.True(t, ok)
	assert.True(t, expired)
	assert.Equal(t, "/x", st.next)

	// A state minted before the retried flag existed (nonce\x00next) still
	// parses, as a first attempt.
	st, expired, ok = parseState(signValue("state", "old-nonce\x00/legacy", time.Now().Add(time.Minute)))
	assert.True(t, ok)
	assert.False(t, expired)
	assert.Equal(t, "old-nonce", st.nonce)
	assert.Equal(t, "/legacy", st.next)
	assert.False(t, st.retried)
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
	state := signState(signinState{nonce: nonce, next: next}, time.Now().Add(time.Minute))
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

// The token exchange must speak GitHub's actual contract: Accept:
// application/json (without it GitHub answers form-encoded) and the four form
// fields of the web flow. Pinned against the fake so a regression cannot hide
// behind a lenient test double.
func TestSigninCallback_ExchangeRequestContract(t *testing.T) {
	var accept, contentType string
	var form url.Values
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			accept = r.Header.Get("Accept")
			contentType = r.Header.Get("Content-Type")
			_ = r.ParseForm()
			form = r.PostForm
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

	state := signState(signinState{nonce: "n1", next: "/x"}, time.Now().Add(time.Minute))
	req := httptest.NewRequest("GET", signinCallbackPath+"?code=abc&state="+url.QueryEscape(state), nil)
	req.Host = "pazer.build"
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: "n1"})
	rec := httptest.NewRecorder()
	handleSigninCallback(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "application/json", accept)
	assert.Equal(t, "application/x-www-form-urlencoded", contentType)
	assert.Equal(t, "cid", form.Get("client_id"))
	assert.Equal(t, "secret", form.Get("client_secret"))
	assert.Equal(t, "abc", form.Get("code"))
	assert.Equal(t, "https://pazer.build/__signin/callback", form.Get("redirect_uri"))
}

// A nonce mismatch (cookie expired, or a second sign-in tab overwrote it) is
// recoverable: the callback restarts the flow through /__signin -- once. The
// restarted state carries the retried marker; if that flow mismatches again the
// user gets a terminal page with a retry link, never a redirect loop.
func TestSigninCallback_NonceMismatch_RestartsOnce(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")

	next := "https://sites.pazer.build/secret/branch/pr-190/"
	state := signState(signinState{nonce: "real-nonce", next: next}, time.Now().Add(time.Minute))
	req := httptest.NewRequest("GET", signinCallbackPath+"?code=abc&state="+url.QueryEscape(state), nil)
	req.Host = "pazer.build"
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: "different-nonce"})
	rec := httptest.NewRecorder()
	handleSigninCallback(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "https://pazer.build"+signinStartPath+"?retry=1&next="+url.QueryEscape(next), rec.Header().Get("Location"))

	// Same failure on an already-retried state: terminal page, no redirect.
	state = signState(signinState{nonce: "real-nonce", next: next, retried: true}, time.Now().Add(time.Minute))
	req = httptest.NewRequest("GET", signinCallbackPath+"?code=abc&state="+url.QueryEscape(state), nil)
	req.Host = "pazer.build"
	rec = httptest.NewRecorder()
	handleSigninCallback(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, rec.Header().Get("Location"), "a retried flow must not restart again")
	body := rec.Body.String()
	assert.Contains(t, body, "Try signing in again")
	assert.Contains(t, body, signinStartPath+"?next="+url.QueryEscape(next))
}

// An expired state (user parked on GitHub's consent screen past the 10-minute
// window, or reloaded a stale callback URL) restarts the flow once, then turns
// terminal -- same loop protection as the nonce mismatch.
func TestSigninCallback_ExpiredState_RestartsOnce(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")

	next := "https://sites.pazer.build/secret/branch/pr-190/"
	state := signState(signinState{nonce: "n1", next: next}, time.Now().Add(-time.Minute))
	req := httptest.NewRequest("GET", signinCallbackPath+"?code=abc&state="+url.QueryEscape(state), nil)
	req.Host = "pazer.build"
	rec := httptest.NewRecorder()
	handleSigninCallback(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "https://pazer.build"+signinStartPath+"?retry=1&next="+url.QueryEscape(next), rec.Header().Get("Location"))

	state = signState(signinState{nonce: "n1", next: next, retried: true}, time.Now().Add(-time.Minute))
	req = httptest.NewRequest("GET", signinCallbackPath+"?code=abc&state="+url.QueryEscape(state), nil)
	req.Host = "pazer.build"
	rec = httptest.NewRecorder()
	handleSigninCallback(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, rec.Header().Get("Location"), "a retried flow must not restart again")
	assert.Contains(t, rec.Body.String(), signinStartPath+"?next="+url.QueryEscape(next))
}

// A state that fails signature verification carries nothing trustworthy, so
// there is no automatic redirect -- but the page still offers a fresh sign-in
// (to the apex root), not a bare dead end.
func TestSigninCallback_ForgedState_TerminalPage(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")

	req := httptest.NewRequest("GET", signinCallbackPath+"?code=abc&state=totally-garbage", nil)
	req.Host = "pazer.build"
	rec := httptest.NewRecorder()
	handleSigninCallback(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, rec.Header().Get("Location"))
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	body := rec.Body.String()
	assert.Contains(t, body, "Try signing in again")
	assert.Contains(t, body, `href="https://pazer.build`+signinStartPath+`"`, "no trusted next: link to a bare sign-in")
}

// GitHub reporting an exchange failure (wrong client secret, consumed or
// expired code) must NOT surface as a 5xx: Cloudflare replaces origin 5xx
// bodies with its own bare error page, which used to strand the user at the
// callback URL with no way forward. Instead: a 4xx page with a retry link.
func TestSigninCallback_ExchangeFailure_RecoverablePage(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GitHub's real shape: HTTP 200 with an error JSON body.
		w.Write([]byte(`{"error":"incorrect_client_credentials","error_description":"The client_id and/or client_secret passed are incorrect."}`))
	}))
	defer gh.Close()
	origToken := githubTokenURL
	githubTokenURL = gh.URL
	defer func() { githubTokenURL = origToken }()

	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")

	next := "https://sites.pazer.build/secret/branch/pr-190/"
	state := signState(signinState{nonce: "n1", next: next}, time.Now().Add(time.Minute))
	req := httptest.NewRequest("GET", signinCallbackPath+"?code=abc&state="+url.QueryEscape(state), nil)
	req.Host = "pazer.build"
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: "n1"})
	rec := httptest.NewRecorder()
	handleSigninCallback(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Less(t, rec.Code, 500, "5xx bodies are replaced by Cloudflare -- never use them here")
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	body := rec.Body.String()
	assert.Contains(t, body, "Try signing in again")
	assert.Contains(t, body, signinStartPath+"?next="+url.QueryEscape(next))
	for _, c := range rec.Result().Cookies() {
		assert.NotEqual(t, sessionCookieName, c.Name, "no session on a failed exchange")
	}
}

// Same recoverable treatment when the code exchange succeeds but reading the
// user's identity (GET /user) fails.
func TestSigninCallback_UserFetchFailure_RecoverablePage(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.Write([]byte(`{"access_token":"gho_test"}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer gh.Close()
	origToken, origAPI := githubTokenURL, githubAPIBase
	githubTokenURL, githubAPIBase = gh.URL, gh.URL
	defer func() { githubTokenURL, githubAPIBase = origToken, origAPI }()

	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")

	next := "https://sites.pazer.build/secret/branch/pr-190/"
	state := signState(signinState{nonce: "n1", next: next}, time.Now().Add(time.Minute))
	req := httptest.NewRequest("GET", signinCallbackPath+"?code=abc&state="+url.QueryEscape(state), nil)
	req.Host = "pazer.build"
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: "n1"})
	rec := httptest.NewRecorder()
	handleSigninCallback(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), signinStartPath+"?next="+url.QueryEscape(next))
}

// The user cancelling on GitHub (?error=access_denied) gets the same actionable
// page -- the state is signature-verified, so the retry link keeps their next.
func TestSigninCallback_GitHubErrorParam_Page(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")

	next := "https://sites.pazer.build/secret/branch/pr-190/"
	state := signState(signinState{nonce: "n1", next: next}, time.Now().Add(time.Minute))
	req := httptest.NewRequest("GET", signinCallbackPath+"?error=access_denied&state="+url.QueryEscape(state), nil)
	req.Host = "pazer.build"
	rec := httptest.NewRecorder()
	handleSigninCallback(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, rec.Body.String(), signinStartPath+"?next="+url.QueryEscape(next))
}

// Every failure path answers with something a browser (behind Cloudflare) can
// act on: a redirect or a 4xx page. Never a 5xx.
func TestSigninCallback_NoFailurePathReturns5xx(t *testing.T) {
	// The fake GitHub accepts only code=good (whose /user then fails); any other
	// code gets GitHub's real failure shape, HTTP 200 with an error JSON body.
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			if r.ParseForm() == nil && r.PostForm.Get("code") == "good" {
				w.Write([]byte(`{"access_token":"gho_test"}`))
				return
			}
			w.Write([]byte(`{"error":"bad_verification_code","error_description":"The code passed is incorrect or expired."}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError) // /user always fails
	}))
	defer gh.Close()
	origToken, origAPI := githubTokenURL, githubAPIBase
	githubTokenURL, githubAPIBase = gh.URL, gh.URL
	defer func() { githubTokenURL, githubAPIBase = origToken, origAPI }()

	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")

	fresh := func(retried bool, exp time.Time) string {
		return url.QueryEscape(signState(signinState{nonce: "n1", next: "/x", retried: retried}, exp))
	}
	future, past := time.Now().Add(time.Minute), time.Now().Add(-time.Minute)
	scenarios := []struct {
		name, query, cookie string
	}{
		{"forged state", "?code=abc&state=garbage", ""},
		{"expired", "?code=abc&state=" + fresh(false, past), ""},
		{"expired retried", "?code=abc&state=" + fresh(true, past), ""},
		{"nonce mismatch", "?code=abc&state=" + fresh(false, future), "other"},
		{"nonce mismatch retried", "?code=abc&state=" + fresh(true, future), "other"},
		{"github error param", "?error=access_denied&state=" + fresh(false, future), ""},
		{"exchange failure", "?code=abc&state=" + fresh(false, future), "n1"},
		{"missing code", "?state=" + fresh(false, future), "n1"},
		{"user fetch failure", "?code=good&state=" + fresh(false, future), "n1"},
	}
	for _, sc := range scenarios {
		req := httptest.NewRequest("GET", signinCallbackPath+sc.query, nil)
		req.Host = "pazer.build"
		if sc.cookie != "" {
			req.AddCookie(&http.Cookie{Name: stateCookieName, Value: sc.cookie})
		}
		rec := httptest.NewRecorder()
		handleSigninCallback(rec, req)
		assert.Less(t, rec.Code, 500, "%s must not 5xx (Cloudflare would mask the body)", sc.name)
		assert.GreaterOrEqual(t, rec.Code, 303, "%s must not succeed", sc.name)
	}
}

// /__signin?retry=1 (the callback's restart redirect) mints a state carrying
// the retried marker, closing the restart loop after one automatic attempt.
func TestSigninStart_RetryMarkerRidesState(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")

	req := httptest.NewRequest("GET", signinStartPath+"?retry=1&next=%2Fp%2Fbranch%2Fb%2F", nil)
	req.Host = "pazer.build"
	rec := httptest.NewRecorder()
	handleSigninStart(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	u, err := url.Parse(rec.Header().Get("Location"))
	require.NoError(t, err)
	st, expired, ok := parseState(u.Query().Get("state"))
	require.True(t, ok)
	assert.False(t, expired)
	assert.True(t, st.retried)
	assert.Equal(t, "/p/branch/b/", st.next)

	// Without retry=1 the marker stays off.
	req = httptest.NewRequest("GET", signinStartPath+"?next=%2Fp%2Fbranch%2Fb%2F", nil)
	req.Host = "pazer.build"
	rec = httptest.NewRecorder()
	handleSigninStart(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	u, err = url.Parse(rec.Header().Get("Location"))
	require.NoError(t, err)
	st, _, ok = parseState(u.Query().Get("state"))
	require.True(t, ok)
	assert.False(t, st.retried)
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
	assert.True(t, canAccess(t, g, "alice", "tok", "PazerOP/allowed"))
	assert.False(t, canAccess(t, g, "alice", "tok", "PazerOP/denied"))
	// Cached: a repeat does not hit GitHub again.
	before := calls
	assert.True(t, canAccess(t, g, "alice", "tok", "PazerOP/allowed"))
	assert.Equal(t, before, calls, "second check should be served from cache")
	// Missing inputs => false, no call.
	assert.False(t, canAccess(t, g, "", "tok", "PazerOP/allowed"))
	assert.False(t, canAccess(t, g, "alice", "", "PazerOP/allowed"))
}

// canAccess collapses canAccessRepo's (allowed, tokenDead) pair to just
// allowed, for tests that only assert access.
func canAccess(t *testing.T, g *GitHubAuth, login, token, repo string) bool {
	t.Helper()
	allowed, _ := g.canAccessRepo(context.Background(), login, token, repo)
	return allowed
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

	// First check hits a transient 500 -> denied, but the non-answer is not
	// cached -- and is NOT classified as a dead token.
	allowed, tokenDead := g.canAccessRepo(context.Background(), "matt", "tok", "PazerOP/UE553")
	assert.False(t, allowed)
	assert.False(t, tokenDead, "a transient failure must not be classified token-dead")
	// GitHub recovers; the next check must re-hit GitHub (not the cache) and now
	// succeed -- the owner is not locked out by the earlier blip.
	status = http.StatusOK
	before := calls
	assert.True(t, canAccess(t, g, "matt", "tok", "PazerOP/UE553"),
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

	// Old, insufficient token: authoritative 404 -> denied (and cached for it).
	assert.False(t, canAccess(t, g, "matt", "scopeless", "PazerOP/UE553"))
	// Re-auth yields a new token with access; it must be re-checked, not shadowed
	// by the cached deny keyed to the previous token.
	assert.True(t, canAccess(t, g, "matt", "good", "PazerOP/UE553"),
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
	allowed, tokenDead := userCanReadProject(ctx, proj)
	assert.False(t, allowed)
	assert.False(t, tokenDead)
}
