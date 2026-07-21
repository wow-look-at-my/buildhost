package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

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
