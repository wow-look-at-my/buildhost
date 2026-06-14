package auth

import (
	"html"
	"net/http"
	"net/url"
	"strings"
)

// loginPath is buildhost's own browser sign-in endpoint. A browser that hits a
// private resource without a token is redirected here (see unauthorizedResponse)
// instead of getting a raw JSON 401 -- the dead end this whole flow fixes. The
// page takes a buildhost token, stores it in an HttpOnly session cookie, and
// sends the browser back to where it came from. Public resources never reach
// this path (they are served anonymously).
const (
	loginPath  = "/__auth/login"
	logoutPath = "/__auth/logout"
)

// loginSubdomains is every service host the sign-in page is registered on, so a
// same-host redirect to loginPath resolves on whichever subdomain served the
// gated resource (sites.{domain}, dl.{domain}, ...). The router's strict host
// partitioning means a bare apex registration would 404 on a subdomain.
var loginSubdomains = []string{"apt", "brew", "dl", "git", "npm", "oci", "sites", "static"}

func init() {
	register := func(pattern string, h http.HandlerFunc) {
		HandleRaw(pattern, h)
		for _, svc := range loginSubdomains {
			ServiceHandleRaw(svc, pattern, h)
		}
	}
	register("GET "+loginPath, handleLoginForm)
	register("POST "+loginPath, handleLoginSubmit)
	register("GET "+logoutPath, handleLogout)
}

// safeNext keeps post-login redirects on this site: it accepts only a same-site
// absolute path, rejecting absolute URLs and scheme-relative ("//evil.com")
// targets so the sign-in page can't be turned into an open redirect.
func safeNext(next string) string {
	if next == "" || next[0] != '/' || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

func handleLoginForm(w http.ResponseWriter, r *http.Request) {
	writeLoginPage(w, http.StatusOK, safeNext(r.URL.Query().Get("next")), "")
}

func handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeLoginPage(w, http.StatusBadRequest, "/", "Could not read the form.")
		return
	}
	next := safeNext(r.PostFormValue("next"))
	token := strings.TrimSpace(r.PostFormValue("token"))
	if !validateLoginToken(r.Context(), token) {
		// Re-render with an error; do not set a cookie for an invalid token.
		writeLoginPage(w, http.StatusUnauthorized, next, "That token is not valid (check it has not expired).")
		return
	}
	setSessionCookie(w, r, token)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w, r)
	http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
}

// writeAccessDeniedPage renders the page shown when a browser is signed in (a
// session cookie is present) but the token still cannot see the resource -- e.g.
// it lacks read scope or is not authorized for this project. It does NOT
// redirect to the sign-in page (that would loop, since the request already
// carries a token); instead it offers links to sign in as a different token or
// to sign out. detail is HTML-escaped.
func writeAccessDeniedPage(w http.ResponseWriter, r *http.Request, status int, detail string) {
	next := r.URL.RequestURI()
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	q := "?next=" + url.QueryEscape(next)
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Access denied -- buildhost</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; background: #0d1117; color: #c9d1d9; margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center; }
  main { max-width: 30rem; padding: 2rem; }
  h1 { font-size: 1.4rem; margin: 0 0 0.75rem; }
  p { line-height: 1.55; margin: 0 0 1rem; color: #8b949e; }
  a { color: #2f81f7; }
</style>
</head>
<body>
<main>
<h1>Access denied</h1>
<p>` + html.EscapeString(detail) + `</p>
<p><a href="` + loginPath + html.EscapeString(q) + `">Sign in with a different token</a> &middot; <a href="` + logoutPath + html.EscapeString(q) + `">Sign out</a></p>
</main>
</body>
</html>
`))
}

// writeLoginPage renders the sign-in form. next is carried through so a
// successful sign-in returns the browser to the originally requested URL; it is
// already sanitized by safeNext. errMsg, when non-empty, is shown to the user.
func writeLoginPage(w http.ResponseWriter, status int, next, errMsg string) {
	// Relax the global default-src 'none' just enough for this page's inline
	// style and same-origin form post -- still no scripts, no external loads.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	errBlock := ""
	if errMsg != "" {
		errBlock = `<p class="err">` + html.EscapeString(errMsg) + `</p>`
	}
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in -- buildhost</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; background: #0d1117; color: #c9d1d9; margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center; }
  main { width: 22rem; max-width: 90vw; padding: 2rem; }
  h1 { font-size: 1.4rem; margin: 0 0 0.5rem; }
  p { line-height: 1.5; margin: 0 0 1rem; color: #8b949e; font-size: 0.92rem; }
  label { display: block; font-size: 0.85rem; margin: 0 0 0.35rem; color: #c9d1d9; }
  input[type=password] { width: 100%; box-sizing: border-box; padding: 0.6rem; border: 1px solid #30363d; border-radius: 0.4rem; background: #161b22; color: #c9d1d9; font-size: 1rem; }
  button { margin-top: 1rem; width: 100%; padding: 0.6rem; border: 0; border-radius: 0.4rem; background: #238636; color: #fff; font-size: 1rem; cursor: pointer; }
  button:hover { background: #2ea043; }
  .err { color: #f85149; }
  code { background: #161b22; padding: 0.1rem 0.3rem; border-radius: 0.25rem; }
</style>
</head>
<body>
<main>
<h1>Sign in</h1>
<p>This resource is private. Paste a buildhost token with <code>read</code> access to view it.</p>
` + errBlock + `<form method="POST" action="` + loginPath + `">
<input type="hidden" name="next" value="` + html.EscapeString(next) + `">
<label for="token">buildhost token</label>
<input id="token" name="token" type="password" autocomplete="current-password" autofocus required>
<button type="submit">Sign in</button>
</form>
</main>
</body>
</html>
`))
}
