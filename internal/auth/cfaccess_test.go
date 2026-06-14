package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

func validCFClaims(iss, aud string) map[string]any {
	return map[string]any{
		"iss":   iss,
		"aud":   aud,
		"email": "user@example.com",
		"exp":   time.Now().Add(10 * time.Minute).Unix(),
		"iat":   time.Now().Unix(),
	}
}

func TestCFAccessVerifier_Disabled(t *testing.T) {
	assert.Nil(t, NewCFAccessVerifier("", "aud"))
	assert.Nil(t, NewCFAccessVerifier("https://team.cloudflareaccess.com", ""))
	assert.NotNil(t, NewCFAccessVerifier("https://team.cloudflareaccess.com", "aud"))
}

func TestCFAccessVerifier_ValidToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	srv := jwksServer(t, &key.PublicKey, "cf-kid") // serves the JWKS at any path, incl. /cdn-cgi/access/certs
	v := NewCFAccessVerifier(srv.URL, "test-aud")
	require.NotNil(t, v)

	tok := signJWT(t, key, "cf-kid", validCFClaims(srv.URL, "test-aud"))
	email, err := v.Verify(context.Background(), tok)
	require.NoError(t, err)
	assert.Equal(t, "user@example.com", email)
}

func TestCFAccessVerifier_RejectsBadAudIssuerExpiry(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	srv := jwksServer(t, &key.PublicKey, "cf-kid")
	v := NewCFAccessVerifier(srv.URL, "test-aud")

	// wrong audience
	_, err = v.Verify(context.Background(), signJWT(t, key, "cf-kid", validCFClaims(srv.URL, "other-aud")))
	assert.Error(t, err)

	// wrong issuer
	_, err = v.Verify(context.Background(), signJWT(t, key, "cf-kid", validCFClaims("https://evil.example", "test-aud")))
	assert.Error(t, err)

	// expired
	expired := validCFClaims(srv.URL, "test-aud")
	expired["exp"] = time.Now().Add(-time.Minute).Unix()
	_, err = v.Verify(context.Background(), signJWT(t, key, "cf-kid", expired))
	assert.Error(t, err)

	// signed by a different key
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	_, err = v.Verify(context.Background(), signJWT(t, other, "cf-kid", validCFClaims(srv.URL, "test-aud")))
	assert.Error(t, err)
}

func TestCFSession_RoundTrip(t *testing.T) {
	val := mintCFSession("a@b.com", time.Now().Add(time.Hour))
	email, ok := verifyCFSession(val)
	assert.True(t, ok)
	assert.Equal(t, "a@b.com", email)

	// tampered
	_, ok = verifyCFSession(val + "x")
	assert.False(t, ok)
	_, ok = verifyCFSession("garbage")
	assert.False(t, ok)

	// expired
	_, ok = verifyCFSession(mintCFSession("a@b.com", time.Now().Add(-time.Minute)))
	assert.False(t, ok)
}

func TestSafeNext(t *testing.T) {
	assert.Equal(t, "/sites/x/", safeNext("/sites/x/"))
	assert.Equal(t, "/", safeNext(""))
	assert.Equal(t, "/", safeNext("//evil.com"))
	assert.Equal(t, "/", safeNext("https://evil.com"))
	assert.Equal(t, "/", safeNext("/\\evil.com"))
}

func TestCFAccessCallback_ValidAssertion_SetsCookieRedirects(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	srv := jwksServer(t, &key.PublicKey, "cf-kid")
	mw.CFAccess = NewCFAccessVerifier(srv.URL, "test-aud")

	tok := signJWT(t, key, "cf-kid", validCFClaims(srv.URL, "test-aud"))
	req := httptest.NewRequest("GET", cfAccessCallbackPath+"?next=%2Fsecret%2Fbranch%2Fpr-190%2F", nil)
	req.Header.Set(cfAccessHeader, tok)
	rec := httptest.NewRecorder()
	handleCFAccessCallback(rec, req)

	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/secret/branch/pr-190/", rec.Header().Get("Location"))
	assert.Contains(t, rec.Header().Get("Set-Cookie"), cfSessionCookieName+"=")

	// The minted cookie authenticates a follow-up request.
	cookie := rec.Result().Cookies()[0]
	email, ok := verifyCFSession(cookie.Value)
	assert.True(t, ok)
	assert.Equal(t, "user@example.com", email)
}

func TestCFAccessCallback_MissingAssertion_401(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.CFAccess = NewCFAccessVerifier("https://team.cloudflareaccess.com", "test-aud")

	req := httptest.NewRequest("GET", cfAccessCallbackPath+"?next=%2F", nil) // no Cf-Access-Jwt-Assertion
	rec := httptest.NewRecorder()
	handleCFAccessCallback(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, rec.Header().Get("Set-Cookie"))
}

func TestCFAccessCallback_NotConfigured_501(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d) // mw.CFAccess nil

	req := httptest.NewRequest("GET", cfAccessCallbackPath, nil)
	rec := httptest.NewRecorder()
	handleCFAccessCallback(rec, req)
	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

// A browser hitting a private resource with no token, when Cloudflare Access is
// configured, is redirected to /__access (off to Cloudflare to authenticate).
func TestRequireProject_PrivateProject_Browser_CFEnabled_RedirectsToAccess(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.CFAccess = NewCFAccessVerifier("https://team.cloudflareaccess.com", "test-aud")

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
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	assert.True(t, strings.HasPrefix(loc, cfAccessCallbackPath+"?next="), "got %q", loc)
	assert.Contains(t, loc, "secret")
}

// End-to-end through the real middleware chain: a valid bh_cfaccess cookie is
// verified by Authenticate, surfaced as a CF identity, and accepted by
// requireProject as read authorization for a private project -- with no token.
func TestCFSessionCookie_AuthenticatesThroughMiddleware(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.CFAccess = NewCFAccessVerifier("https://team.cloudflareaccess.com", "test-aud")

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
	req.AddCookie(&http.Cookie{Name: cfSessionCookieName, Value: mintCFSession("user@example.com", time.Now().Add(time.Hour))})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.True(t, called, "valid session cookie should authenticate through the middleware")
	assert.Equal(t, http.StatusOK, rec.Code)

	// A tampered cookie does not authenticate.
	called = false
	req2 := httptest.NewRequest("GET", "/secret/branch/pr-190/", nil)
	req2.AddCookie(&http.Cookie{Name: cfSessionCookieName, Value: mintCFSession("user@example.com", time.Now().Add(time.Hour)) + "x"})
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	assert.False(t, called, "tampered cookie must not authenticate")
	assert.Equal(t, http.StatusUnauthorized, rec2.Code)
}

// A request carrying a verified Cloudflare Access identity may read a private
// resource without any project token.
func TestRequireProject_PrivateProject_CFAuthenticated_Allows(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	mw.CFAccess = NewCFAccessVerifier("https://team.cloudflareaccess.com", "test-aud")

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
	handler := requireProjectFunc(parse, inner)

	req := httptest.NewRequest("GET", "/secret/branch/pr-190/", nil)
	req = req.WithContext(WithCFAccess(req.Context(), "user@example.com"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called, "CF-authenticated read should be allowed")
	assert.Equal(t, http.StatusOK, rec.Code)
}
