package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitHubAuth_Disabled(t *testing.T) {
	t.Serial()
	assert.Nil(t, NewGitHubAuth("", "sec"))
	assert.Nil(t, NewGitHubAuth("id", ""))
	assert.NotNil(t, NewGitHubAuth("id", "sec"))
}

func TestSession_RoundTrip(t *testing.T) {
	t.Serial()
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
	t.Serial()
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
	st, expired, ok = parseState(signState(signinState{nonce: "n", next: "/x"}, time.Now().Add(-time.Minute)))
	assert.True(t, ok)
	assert.True(t, expired)
	assert.Equal(t, "/x", st.next)

	// A state minted before the retried flag existed (nonce\x00next) still
	st, expired, ok = parseState(signValue("state", "old-nonce\x00/legacy", time.Now().Add(time.Minute)))
	assert.True(t, ok)
	assert.False(t, expired)
	assert.Equal(t, "old-nonce", st.nonce)
	assert.Equal(t, "/legacy", st.next)
	assert.False(t, st.retried)
}

func TestSafeNextURL(t *testing.T) {
	t.Serial()
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
	t.Serial()
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
	t.Serial()
	d := openTestDB(t)
	initTestMiddleware(t, d)
	req := httptest.NewRequest("GET", signinStartPath, nil)
	rec := httptest.NewRecorder()
	handleSigninStart(rec, req)
	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestSigninCallback_ValidLogin_SetsSession(t *testing.T) {
	t.Serial()
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
func TestSigninCallback_ExchangeRequestContract(t *testing.T) {
	t.Serial()
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

func TestSigninCallback_NonceMismatch_RestartsOnce(t *testing.T) {
	t.Serial()
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

func TestSigninCallback_ExpiredState_RestartsOnce(t *testing.T) {
	t.Serial()
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

func TestSigninStart_RetryMarkerRidesState(t *testing.T) {
	t.Serial()
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
