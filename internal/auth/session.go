package auth

import (
	"context"
	"net/http"
)

// sessionCookieName carries a buildhost token set by the browser sign-in flow
// (internal/auth/login.go). It lets a human view a private resource (e.g. a
// private static-site PR preview) in a browser without an Authorization header:
// the cookie value is a token, read by ExtractToken as the lowest-priority
// source. It is HttpOnly + SameSite=Lax + Secure (on https), so it is not
// script-readable and is never sent on a cross-site write, which keeps the
// existing token-auth model (and its CSRF posture) intact.
const sessionCookieName = "bh_session"

// sessionCookieMaxAge bounds how long the browser keeps a sign-in. The token's
// own expiry is still enforced on every request at lookup time; this only caps
// the convenience window.
const sessionCookieMaxAge = 12 * 60 * 60 // 12h

func sessionCookieFrom(r *http.Request) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionCookieMaxAge,
		HttpOnly: true,
		Secure:   RequestScheme(r) == "https",
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   RequestScheme(r) == "https",
		SameSite: http.SameSiteLaxMode,
	})
}

// validateLoginToken reports whether raw authenticates as a real, non-expired
// token (a static API token or a verifiable OIDC JWT). It checks authentication
// only -- per-resource authorization is still enforced by requireProject on the
// actual request, so a token that is valid but not authorized for the target
// project signs in yet is refused at the resource (handled without a redirect
// loop in unauthorizedResponse).
func validateLoginToken(ctx context.Context, raw string) bool {
	if raw == "" || mw == nil {
		return false
	}
	if _, err := mw.DB.LookupToken(ctx, raw); err == nil {
		return true
	}
	if LooksLikeJWT(raw) && mw.Verifier != nil {
		policies, _ := mw.DB.ListOIDCPolicies(ctx)
		var vr VerifyResult
		if _, _, err := mw.Verifier.VerifyTokenFull(ctx, raw, policies, &vr); err == nil {
			return true
		}
	}
	return false
}
