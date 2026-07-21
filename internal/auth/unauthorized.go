package auth

// Responses for unauthenticated and unauthorized requests: the canonical
// project 404, the 401 (JSON, OCI-challenge, or browser sign-in redirect),
// and the signed-in-but-forbidden HTML page. Split from middleware.go, which
// holds the auth middleware and the requireProject flow.

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

// projectNotFound writes the canonical 404 for a project that does not exist or
// that the caller may not see. Both cases share this exact response so a hidden
// (HiddenReadAccess) read cannot be used to probe for the existence of private
// projects.
func projectNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(`{"error":"project not found"}`))
}

func unauthorizedResponse(w http.ResponseWriter, r *http.Request) {
	msg := "authentication required"
	if err := OIDCErrorFrom(r.Context()); err != nil {
		// A JWT was presented and rejected -- say why (audience, org allowlist,
		// event, expiry, signature, ...) instead of a bare message, so a CI
		// caller can see what to fix.
		msg += ": OIDC token rejected: " + err.Error()
	}

	if strings.HasPrefix(r.URL.Path, "/v2/") {
		// OCI clients (docker pull/push) require the registry error envelope and
		// a Basic challenge on /v2/ so they retry with credentials.
		w.Header().Set("Www-Authenticate", `Basic realm="buildhost"`)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		body, _ := json.Marshal(map[string]any{
			"errors": []map[string]string{{"code": "UNAUTHORIZED", "message": msg}},
		})
		w.Write(body)
		return
	}

	// Browser handling when "Sign in with GitHub" is configured. Programmatic
	// clients (no text/html) -- and deployments without GitHub login configured --
	// fall through to the plain JSON 401 below, unchanged.
	if prefersHTML(r) && githubAuthEnabled() {
		login, signedIn := UserFrom(r.Context())
		switch {
		case !signedIn && TokenFrom(r.Context()) == nil:
			// Anonymous browser: send them to GitHub to sign in, returning to the
			// resource afterward. On a site-domain host the sign-in entrypoint is
			// the PRIMARY apex (the OAuth callback lives there); when no primary
			// domain is configured that hop is unavailable and loginRedirectURL
			// returns "" -- fall through to the plain JSON 401 below.
			if target := loginRedirectURL(r); target != "" {
				http.Redirect(w, r, target, http.StatusSeeOther)
				return
			}
		case signedIn:
			// Signed in, but not authorized for this resource (their GitHub account
			// can't read the backing repo, or the project has no repo recorded).
			// Re-redirecting to /__signin would loop -- GitHub re-auths the same
			// account and bounces straight back -- so render an actionable page
			// (who you are, what access is needed, sign out to switch accounts)
			// instead of the dead-end JSON 401 a browser cannot act on.
			signedInForbiddenHTML(w, r, login, ProjectFrom(r.Context()))
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	body, _ := json.Marshal(map[string]string{"error": msg})
	w.Write(body)
}

// signedInForbiddenHTML renders an actionable page for a browser that IS signed
// in with GitHub but is not authorized to read this resource. The anonymous case
// redirects to /__signin; this one must NOT -- the user already holds a valid
// session, so a redirect would bounce straight back here (GitHub re-auths the
// same account), an infinite loop. So we explain the situation and offer a
// sign-out, letting them switch to an account that has access. It returns 403
// (authenticated but not permitted), not 401.
func signedInForbiddenHTML(w http.ResponseWriter, r *http.Request, login string, project *db.Project) {
	esc := template.HTMLEscapeString
	var detail string
	if project != nil && project.GithubRepo != "" {
		detail = "Your GitHub account <strong>" + esc(login) + "</strong> doesn't have access to <strong>" +
			esc(project.GithubRepo) + "</strong>, the repository behind this resource. " +
			"Switch to an account that can, or ask the owner for access."
	} else {
		detail = "You're signed in as <strong>" + esc(login) + "</strong>, but this resource isn't shared " +
			"through GitHub sign-in. Ask the owner for a project access token or a temporary download link."
	}

	// Relax the global default-src 'none' just enough for the one inline <style>;
	// no scripts, no external resources (same approach as the web frontend).
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Access denied</title>
<style>
  body { font-family: system-ui, -apple-system, sans-serif; max-width: 34rem; margin: 12vh auto; padding: 0 1.25rem; line-height: 1.55; }
  h1 { font-size: 1.4rem; margin-bottom: .5rem; }
  a.btn { display: inline-block; margin-top: 1rem; padding: .55rem .9rem; border: 1px solid; border-radius: .4rem; text-decoration: none; }
  .hint { margin-top: 1.25rem; font-size: .85rem; opacity: .8; }
</style>
</head>
<body>
<h1>Access denied</h1>
<p>%s</p>
<div><a class="btn" href="%s">Sign out &amp; switch account</a></div>
<p class="hint">To use a different account you may also need to <a href="https://github.com/logout">sign out of GitHub</a> first.</p>
</body>
</html>
`, detail, esc(signoutURL(r)))
}

// prefersHTML reports whether the request came from a browser navigation (its
// Accept header lists text/html). Used to decide whether an auth failure should
// drive the Cloudflare Access sign-in redirect versus the raw JSON that
// programmatic clients expect.
func prefersHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}
