package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractToken_SessionCookie_LowestPriority(t *testing.T) {
	// Cookie alone is used when nothing else is present.
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "cookie-tok"})
	assert.Equal(t, "cookie-tok", ExtractToken(req))

	// An explicit Authorization header overrides the cookie.
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "cookie-tok"})
	req2.Header.Set("Authorization", "Bearer header-tok")
	assert.Equal(t, "header-tok", ExtractToken(req2))
}

func TestSafeNext(t *testing.T) {
	assert.Equal(t, "/sites/x/", safeNext("/sites/x/"))
	assert.Equal(t, "/", safeNext(""))
	assert.Equal(t, "/", safeNext("//evil.com"))       // scheme-relative open redirect
	assert.Equal(t, "/", safeNext("https://evil.com")) // absolute URL
	assert.Equal(t, "/", safeNext("/\\evil.com"))      // backslash trick
}

func TestLoginForm_RendersHTML(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", loginPath+"?next=%2Fsecret%2Fbranch%2Fpr-190%2F", nil)
	handleLoginForm(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	body := rec.Body.String()
	assert.Contains(t, body, "<form")
	assert.Contains(t, body, `name="token"`)
	assert.Contains(t, body, "/secret/branch/pr-190/") // next carried into the form
}

func TestLoginSubmit_ValidToken_SetsCookieAndRedirects(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	plaintext, _, err := d.CreateToken(context.Background(), "viewer", nil, "read")
	require.NoError(t, err)

	form := url.Values{"token": {plaintext}, "next": {"/secret/branch/pr-190/"}}
	req := httptest.NewRequest("POST", loginPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleLoginSubmit(rec, req)

	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/secret/branch/pr-190/", rec.Header().Get("Location"))
	setCookie := rec.Header().Get("Set-Cookie")
	assert.Contains(t, setCookie, sessionCookieName+"="+plaintext)
	assert.Contains(t, setCookie, "HttpOnly")
}

func TestLoginSubmit_InvalidToken_NoCookie(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	form := url.Values{"token": {"not-a-real-token"}, "next": {"/secret/"}}
	req := httptest.NewRequest("POST", loginPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleLoginSubmit(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, rec.Header().Get("Set-Cookie"))
	assert.Contains(t, rec.Body.String(), "not valid")
}

func TestLoginSubmit_OpenRedirectBlocked(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	plaintext, _, err := d.CreateToken(context.Background(), "viewer", nil, "read")
	require.NoError(t, err)

	form := url.Values{"token": {plaintext}, "next": {"https://evil.com"}}
	req := httptest.NewRequest("POST", loginPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleLoginSubmit(rec, req)

	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/", rec.Header().Get("Location")) // sanitized, not evil.com
}

func TestLogout_ClearsCookie(t *testing.T) {
	req := httptest.NewRequest("GET", logoutPath+"?next=%2F", nil)
	rec := httptest.NewRecorder()
	handleLogout(rec, req)

	assert.Equal(t, http.StatusSeeOther, rec.Code)
	setCookie := rec.Header().Get("Set-Cookie")
	assert.Contains(t, setCookie, sessionCookieName+"=")
	assert.Contains(t, setCookie, "Max-Age=0") // deletion
}
