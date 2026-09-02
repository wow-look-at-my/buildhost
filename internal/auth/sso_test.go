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

// setTestSiteDomain configures the site/primary domains for the duration of a
// test, mirroring what auth.Init does from config.
func setTestSiteDomain(t *testing.T, site, primary string) {
	t.Helper()
	origS, origP := sharedSiteDomain, sharedPrimaryDomain
	sharedSiteDomain, sharedPrimaryDomain = site, primary
	t.Cleanup(func() { sharedSiteDomain, sharedPrimaryDomain = origS, origP })
}

func TestSiteApexOf(t *testing.T) {
	setTestSiteDomain(t, "pazer.site", "pazer.build")
	assert.Equal(t, "pazer.site", siteApexOf("pazer.site"))
	assert.Equal(t, "pazer.site", siteApexOf("myapp.pazer.site"))
	assert.Equal(t, "pazer.site", siteApexOf("MyApp.PAZER.SITE"), "hosts fold case like DNS")
	assert.Equal(t, "", siteApexOf("a.b.pazer.site"), "exactly one label under the domain")
	assert.Equal(t, "", siteApexOf(".pazer.site"))
	assert.Equal(t, "", siteApexOf("pazer.build"))
	assert.Equal(t, "", siteApexOf("x.pazer.site.evil.com"), "suffix lookalike must not classify")
	assert.Equal(t, "", siteApexOf("evilpazer.site"), "label boundary is a real dot")

	setTestSiteDomain(t, "", "")
	assert.Equal(t, "", siteApexOf("myapp.pazer.site"), "unset feature classifies nothing")
}

func TestSafeNextURL_SiteDomain(t *testing.T) {
	setTestSiteDomain(t, "pazer.site", "pazer.build")
	r := httptest.NewRequest("GET", "/__signin", nil)
	r.Host = "pazer.build"

	// Site-domain destinations are part of this deployment: accepted.
	assert.Equal(t, "https://myapp.pazer.site/f", safeNextURL(r, "https://myapp.pazer.site/f"))
	assert.Equal(t, "https://pazer.site/projects/x", safeNextURL(r, "https://pazer.site/projects/x"))

	// Open-redirect shapes still rejected.
	assert.Equal(t, "https://pazer.build/", safeNextURL(r, "https://evil.com/"))
	assert.Equal(t, "https://pazer.build/", safeNextURL(r, "https://x.pazer.site.evil.com/"))
	assert.Equal(t, "https://pazer.build/", safeNextURL(r, "https://a.b.pazer.site/"), "only one label under the site domain")

	// Existing apex/subdomain acceptance unchanged.
	assert.Equal(t, "https://dl.pazer.build/x", safeNextURL(r, "https://dl.pazer.build/x"))

	// Feature off: site URLs are foreign hosts again.
	setTestSiteDomain(t, "", "")
	assert.Equal(t, "https://pazer.build/", safeNextURL(r, "https://myapp.pazer.site/f"))
}

func TestApexClassifiers_SiteDomain(t *testing.T) {
	setTestSiteDomain(t, "pazer.site", "pazer.build")

	r := httptest.NewRequest("GET", "/x", nil)
	r.Host = "myapp.pazer.site"
	assert.Equal(t, "https://pazer.site", apexRootURL(r))
	assert.Equal(t, "pazer.site", apexHost(r))
	assert.Equal(t, "dl.pazer.site", ApexServiceURL(r, "dl").Host)

	// The bare site apex is its own apex.
	r.Host = "pazer.site"
	assert.Equal(t, "https://pazer.site", apexRootURL(r))
	assert.Equal(t, "pazer.site", apexHost(r))

	// Non-site hosts keep the existing service-label behavior.
	r.Host = "dl.pazer.build"
	assert.Equal(t, "https://pazer.build", apexRootURL(r))
}

func TestLoginRedirectURL_SiteHost(t *testing.T) {
	setTestSiteDomain(t, "pazer.site", "pazer.build")
	r := httptest.NewRequest("GET", "/secret/file.html", nil)
	r.Host = "myapp.pazer.site"
	assert.Equal(t,
		"https://pazer.build"+signinStartPath+"?next="+url.QueryEscape("https://myapp.pazer.site/secret/file.html"),
		loginRedirectURL(r),
		"site-domain sign-in must target the primary apex (the OAuth callback lives there)")

	// No primary domain: the hop is unavailable.
	setTestSiteDomain(t, "pazer.site", "")
	assert.Equal(t, "", loginRedirectURL(r))

	// Non-site hosts unchanged.
	r2 := httptest.NewRequest("GET", "/f", nil)
	r2.Host = "sites.pazer.build"
	assert.Equal(t,
		"https://pazer.build"+signinStartPath+"?next="+url.QueryEscape("https://sites.pazer.build/f"),
		loginRedirectURL(r2))
}

func TestRequireProject_SiteDomainBrowser_RedirectsToPrimary(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")
	setTestSiteDomain(t, "pazer.site", "pazer.build")

	proj := &db.Project{Name: "secret", IsPrivate: true, Versioning: "auto"}
	require.NoError(t, d.CreateProject(context.Background(), proj))
	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "secret", access: ReadAccess}
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	handler := requireProjectFunc(parse, inner)

	req := httptest.NewRequest("GET", "/preview/index.html", nil)
	req.Host = "secret.pazer.site"
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t,
		"https://pazer.build"+signinStartPath+"?next="+url.QueryEscape("https://secret.pazer.site/preview/index.html"),
		rec.Header().Get("Location"))

	setTestSiteDomain(t, "pazer.site", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Empty(t, rec.Header().Get("Location"))
}

// The instant path: a browser that already holds a valid primary-apex session
// (with a LIVE GitHub token -- probed via GET /user) and asks to sign in to a
// site-domain destination skips the OAuth consent round-trip -- the session is
// parked and the browser is bounced straight to /__sso. The full redemption
// round trip must set the SAME session value as a site-apex cookie, and the
// code must be single-use.
func TestSigninStart_InstantHandoff_RoundTrip(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			w.Write([]byte(`{"login":"alice"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer gh.Close()
	origAPI := githubAPIBase
	githubAPIBase = gh.URL
	defer func() { githubAPIBase = origAPI }()

	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")
	setTestSiteDomain(t, "pazer.site", "pazer.build")

	session := mintSession("alice", "gho_tok", time.Now().Add(time.Hour))
	next := "https://myapp.pazer.site/secret/index.html"

	req := httptest.NewRequest("GET", signinStartPath+"?next="+url.QueryEscape(next), nil)
	req.Host = "pazer.build"
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	rec := httptest.NewRecorder()
	handleSigninStart(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	require.NoError(t, err)
	assert.Equal(t, "myapp.pazer.site", u.Host, "handoff redeems on the destination host")
	assert.Equal(t, ssoPath, u.Path)
	code := u.Query().Get("code")
	require.NotEmpty(t, code)
	assert.Equal(t, next, u.Query().Get("next"))

	// The session token must never ride a URL: neither the raw GitHub token nor
	assert.NotContains(t, loc, "gho_tok")
	assert.NotContains(t, loc, session)

	// Redeem on the site domain.
	redeem := httptest.NewRequest("GET", u.RequestURI(), nil)
	redeem.Host = "myapp.pazer.site"
	rec = httptest.NewRecorder()
	handleSSORedeem(rec, redeem)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, next, rec.Header().Get("Location"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	var sc *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			sc = c
		}
	}
	require.NotNil(t, sc, "redemption must set the session cookie")
	assert.Equal(t, "pazer.site", sc.Domain, "cookie is scoped to the SITE apex")
	assert.Equal(t, session, sc.Value, "the parked session value is handed across verbatim")
	assert.True(t, sc.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, sc.SameSite)

	rec = httptest.NewRecorder()
	handleSSORedeem(rec, redeem.Clone(context.Background()))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "already used")
	for _, c := range rec.Result().Cookies() {
		assert.NotEqual(t, sessionCookieName, c.Name, "no session on a replayed code")
	}
	// The failure page offers a restart at the PRIMARY apex carrying the
	assert.Contains(t, rec.Body.String(), "https://pazer.build"+signinStartPath)
}

// A session whose embedded GitHub token is DEAD must not ride the instant
// path: handing it across would loop (the site's dead-session re-auth bounces
// back to /__signin, whose still-MAC-valid apex cookie would mint the same
// dead session forever). The liveness probe fails and the flow falls through
// to full OAuth, which mints a fresh session for the handoff.
func TestSigninStart_InstantHandoff_DeadTokenFallsThroughToOAuth(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // GET /user: token revoked
	}))
	defer gh.Close()
	origAPI := githubAPIBase
	githubAPIBase = gh.URL
	defer func() { githubAPIBase = origAPI }()

	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")
	setTestSiteDomain(t, "pazer.site", "pazer.build")

	next := "https://myapp.pazer.site/f"
	req := httptest.NewRequest("GET", signinStartPath+"?next="+url.QueryEscape(next), nil)
	req.Host = "pazer.build"
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: mintSession("alice", "gho_dead", time.Now().Add(time.Hour))})
	rec := httptest.NewRecorder()
	handleSigninStart(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	assert.True(t, strings.HasPrefix(loc, githubAuthorizeURL+"?"),
		"a dead-token session must fall through to full OAuth, got %q", loc)
	assert.NotContains(t, loc, ssoPath)
}

// The OAuth-callback path: completing GitHub sign-in with a site-domain next
// sets the primary-apex session cookie as always AND bounces through /__sso
// instead of redirecting to the destination directly.
func TestSigninCallback_SiteNext_MintsHandoff(t *testing.T) {
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
	setTestSiteDomain(t, "pazer.site", "pazer.build")

	nonce := "nonce-sso"
	next := "https://myapp.pazer.site/preview/"
	state := signState(signinState{nonce: nonce, next: next}, time.Now().Add(time.Minute))
	req := httptest.NewRequest("GET", signinCallbackPath+"?code=abc&state="+url.QueryEscape(state), nil)
	req.Host = "pazer.build"
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: nonce})
	rec := httptest.NewRecorder()
	handleSigninCallback(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	require.NoError(t, err)
	assert.Equal(t, "myapp.pazer.site", u.Host)
	assert.Equal(t, ssoPath, u.Path)
	assert.Equal(t, next, u.Query().Get("next"))

	// The primary-apex cookie is still minted (future sign-ins are instant).
	var apexCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			apexCookie = c
		}
	}
	require.NotNil(t, apexCookie)
	assert.Equal(t, "pazer.build", apexCookie.Domain)

	// No token material in the redirect.
	assert.NotContains(t, loc, "gho_test")
	assert.NotContains(t, loc, apexCookie.Value)

	// Redeeming hands across exactly the session the callback minted.
	redeem := httptest.NewRequest("GET", u.RequestURI(), nil)
	redeem.Host = "myapp.pazer.site"
	rec = httptest.NewRecorder()
	handleSSORedeem(rec, redeem)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	var siteCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			siteCookie = c
		}
	}
	require.NotNil(t, siteCookie)
	assert.Equal(t, "pazer.site", siteCookie.Domain)
	assert.Equal(t, apexCookie.Value, siteCookie.Value)

	// A same-apex next keeps the direct redirect (no handoff hop).
	state = signState(signinState{nonce: nonce, next: "https://sites.pazer.build/p/branch/b/"}, time.Now().Add(time.Minute))
	req = httptest.NewRequest("GET", signinCallbackPath+"?code=abc&state="+url.QueryEscape(state), nil)
	req.Host = "pazer.build"
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: nonce})
	rec = httptest.NewRecorder()
	handleSigninCallback(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "https://sites.pazer.build/p/branch/b/", rec.Header().Get("Location"))
}

func TestSSORedeem_ExpiredCode(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	setTestSiteDomain(t, "pazer.site", "pazer.build")

	origTTL := ssoHandoffTTL
	ssoHandoffTTL = -time.Second // mint already-expired codes
	t.Cleanup(func() { ssoHandoffTTL = origTTL })

	next := "https://myapp.pazer.site/f"
	target := mintSiteHandoff(mintSession("alice", "tok", time.Now().Add(time.Hour)), next)
	require.NotEmpty(t, target)
	u, err := url.Parse(target)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", u.RequestURI(), nil)
	req.Host = "myapp.pazer.site"
	rec := httptest.NewRecorder()
	handleSSORedeem(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "expired")
	// The next was MAC-verified, so the restart link may carry it -- at the
	assert.Contains(t, rec.Body.String(), "https://pazer.build"+signinStartPath)
	for _, c := range rec.Result().Cookies() {
		assert.NotEqual(t, sessionCookieName, c.Name)
	}
}

func TestSSORedeem_TamperRejected(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	setTestSiteDomain(t, "pazer.site", "pazer.build")

	next := "https://myapp.pazer.site/f"
	target := mintSiteHandoff(mintSession("alice", "tok", time.Now().Add(time.Hour)), next)
	require.NotEmpty(t, target)
	u, err := url.Parse(target)
	require.NoError(t, err)
	code := u.Query().Get("code")

	// Swapping the query next for another destination fails: the real next is
	req := httptest.NewRequest("GET", ssoPath+"?code="+url.QueryEscape(code)+"&next="+url.QueryEscape("https://evil.pazer.site/steal"), nil)
	req.Host = "myapp.pazer.site"
	rec := httptest.NewRecorder()
	handleSSORedeem(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Tampering with the code itself fails the MAC.
	req = httptest.NewRequest("GET", ssoPath+"?code="+url.QueryEscape(code+"x"), nil)
	req.Host = "myapp.pazer.site"
	rec = httptest.NewRecorder()
	handleSSORedeem(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// The untampered code still redeems (the failures above consumed nothing).
	req = httptest.NewRequest("GET", u.RequestURI(), nil)
	req.Host = "myapp.pazer.site"
	rec = httptest.NewRecorder()
	handleSSORedeem(rec, req)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
}

func TestSSORedeem_HostGate(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	setTestSiteDomain(t, "pazer.site", "pazer.build")

	next := "https://myapp.pazer.site/f"
	target := mintSiteHandoff(mintSession("alice", "tok", time.Now().Add(time.Hour)), next)
	u, err := url.Parse(target)
	require.NoError(t, err)

	// /__sso does not exist outside the site domain (the host-agnostic
	// registration answers everywhere, so the handler itself must gate).
	for _, host := range []string{"pazer.build", "a.b.pazer.site", "evil.com"} {
		req := httptest.NewRequest("GET", u.RequestURI(), nil)
		req.Host = host
		rec := httptest.NewRecorder()
		handleSSORedeem(rec, req)
		assert.Equalf(t, http.StatusNotFound, rec.Code, "host %s", host)
	}

	// The bare site apex is a valid redemption host (host-agnostic fallthrough
	req := httptest.NewRequest("GET", u.RequestURI(), nil)
	req.Host = "pazer.site"
	rec := httptest.NewRecorder()
	handleSSORedeem(rec, req)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
}

// Full flow through the real router (host dispatch included), ending with the
// handed-over cookie authorizing a private-project read on the site domain.
func TestSSOHandoff_EndToEnd_AuthorizesSiteDomainRead(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/PazerOP/tesla-wheel-data":
			w.WriteHeader(http.StatusOK)
		case "/user":
			w.Write([]byte(`{"login":"alice"}`)) // the instant path's liveness probe
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer gh.Close()
	orig := githubAPIBase
	githubAPIBase = gh.URL
	defer func() { githubAPIBase = orig }()

	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.GitHub = NewGitHubAuth("cid", "secret")
	setTestSiteDomain(t, "pazer.site", "pazer.build")
	registerSSOHandoffRoutes(sharedSiteDomain) // Init-time registration, done by hand here

	routerHandler := mw.Authenticate(http.HandlerFunc(ServeHTTP))

	session := mintSession("alice", "gho_tok", time.Now().Add(time.Hour))
	next := "https://tesla-wheel-data.pazer.site/index.html"
	req := httptest.NewRequest("GET", signinStartPath+"?next="+url.QueryEscape(next), nil)
	req.Host = "pazer.build"
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	rec := httptest.NewRecorder()
	routerHandler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	u, err := url.Parse(rec.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, ssoPath, u.Path)

	req = httptest.NewRequest("GET", u.RequestURI(), nil)
	req.Host = "tesla-wheel-data.pazer.site"
	rec = httptest.NewRecorder()
	routerHandler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, next, rec.Header().Get("Location"))
	var siteCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			siteCookie = c
		}
	}
	require.NotNil(t, siteCookie)
	require.Equal(t, "pazer.site", siteCookie.Domain)

	proj := &db.Project{Name: "tesla-wheel-data", IsPrivate: true, Versioning: "auto", GithubRepo: "PazerOP/tesla-wheel-data"}
	require.NoError(t, d.CreateProject(context.Background(), proj))
	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "tesla-wheel-data", access: ReadAccess}
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	gated := mw.Authenticate(requireProjectFunc(parse, inner))
	req = httptest.NewRequest("GET", "/index.html", nil)
	req.Host = "tesla-wheel-data.pazer.site"
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: siteCookie.Value})
	rec = httptest.NewRecorder()
	gated.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}
