package auth

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Sign in with GitHub.

// GitHub OAuth endpoints. Vars (not consts) so tests can point them at a local
// server; never reassigned in production.
var (
	githubAuthorizeURL = "https://github.com/login/oauth/authorize"
	githubTokenURL     = "https://github.com/login/oauth/access_token"
	githubAPIBase      = "https://api.github.com"
)

const (
	signinStartPath    = "/__signin"
	signinCallbackPath = "/__signin/callback"
	signoutPath        = "/__signout"

	sessionCookieName = "bh_session"     // domain-wide; holds a signed login+exp
	stateCookieName   = "bh_oauth_state" // short-lived CSRF nonce
	sessionMaxAge     = 12 * 60 * 60     // 12h
	stateMaxAge       = 10 * 60          // 10m to complete the round-trip
)

func init() {
	HandleRaw("GET "+signinStartPath, handleSigninStart)
	HandleRaw("GET "+signinCallbackPath, handleSigninCallback)
	HandleRaw("GET "+signoutPath, handleSignout)
}

// GitHubAuth performs the OAuth Authorization Code flow against GitHub. A
// signed-in user is authorized for a private project by their access to that
type GitHubAuth struct {
	clientID     string
	clientSecret string
	http         *http.Client

	mu        sync.Mutex
	repoCache map[string]repoAccess // key: login\x00owner/repo
}

type repoAccess struct {
	result repoCheckResult
	exp    time.Time
}

const repoAccessTTL = 5 * time.Minute

type repoCheckResult int

const (
	repoCheckTransient repoCheckResult = iota
	repoCheckAllowed
	repoCheckNoAccess
	repoCheckTokenDead
)

// NewGitHubAuth returns a configured GitHubAuth, or nil if either the client id
// or secret is empty (the feature is then disabled and browsers fall back to the
func NewGitHubAuth(clientID, clientSecret string) *GitHubAuth {
	clientID = strings.TrimSpace(clientID)
	clientSecret = strings.TrimSpace(clientSecret)
	if clientID == "" || clientSecret == "" {
		return nil
	}
	return &GitHubAuth{
		clientID:     clientID,
		clientSecret: clientSecret,
		http:         &http.Client{Timeout: 15 * time.Second},
		repoCache:    make(map[string]repoAccess),
	}
}

func githubAuth() *GitHubAuth {
	if mw == nil {
		return nil
	}
	return mw.GitHub
}

func githubAuthEnabled() bool { return githubAuth() != nil }

// loginRedirectURL is the absolute URL a browser needing to authenticate is sent
// to: the apex sign-in entrypoint, carrying a next= back to the full original
// URL (which may be on a service subdomain). A request on the configured site
func loginRedirectURL(r *http.Request) string {
	next := RequestBaseURL(r) + r.URL.RequestURI()
	base := apexRootURL(r)
	if requestSiteApex(r) != "" {
		pd := PrimaryDomain()
		if pd == "" {
			return ""
		}
		base = RequestScheme(r) + "://" + pd
	}
	return base + signinStartPath + "?next=" + url.QueryEscape(next)
}

// signoutURL is the apex sign-out entrypoint, carrying a next= back to the full
// original URL. After clearing the session the browser returns to the resource,
func signoutURL(r *http.Request) string {
	next := RequestBaseURL(r) + r.URL.RequestURI()
	return apexRootURL(r) + signoutPath + "?next=" + url.QueryEscape(next)
}

// apexRootURL returns scheme://<apex>, deriving the apex from the request Host by
// stripping a known leading service label (apt/dl/sites/...). Correct whether
// called from a service subdomain (strips it) or the apex itself (nothing to
func apexRootURL(r *http.Request) string {
	host, port := r.Host, ""
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host, port = host[:i], host[i:]
	}
	if sd := siteApexOf(host); sd != "" {
		host = sd
	} else if dot := strings.IndexByte(host, '.'); dot > 0 && knownServiceLabels.Contains(host[:dot]) {
		host = host[dot+1:]
	}
	return RequestScheme(r) + "://" + host + port
}

// callbackURL is the fixed redirect_uri registered with the GitHub OAuth app:
func callbackURL(r *http.Request) string {
	return RequestBaseURL(r) + signinCallbackPath
}

func handleSigninStart(w http.ResponseWriter, r *http.Request) {
	g := githubAuth()
	if g == nil {
		http.Error(w, "Sign in is not configured on this server.", http.StatusNotImplemented)
		return
	}
	next := safeNextURL(r, r.URL.Query().Get("next"))
	// Instant cross-domain handoff: the browser already holds a valid session on
	// this apex and is heading to a site-domain destination -- skip the OAuth
	// consent round-trip and hand the existing session across via /__sso. The
	// session MAC alone is not enough to hand over: its embedded GitHub token
	if c, err := r.Cookie(sessionCookieName); err == nil {
		if _, ghToken, ok := verifySession(c.Value); ok && siteHandoffDest(next) != nil {
			if _, lerr := g.fetchLogin(r.Context(), ghToken); lerr == nil {
				if target := mintSiteHandoff(c.Value, next); target != "" {
					http.Redirect(w, r, target, http.StatusSeeOther)
					return
				}
			}
		}
	}
	retried := r.URL.Query().Get("retry") == "1"

	nonce := randToken()
	// Bind the destination into a signed state and tie the flow to this browser
	// via a short-lived cookie (double-submit), so the callback can't be forged
	// or pointed elsewhere.
	http.SetCookie(w, &http.Cookie{
		Name: stateCookieName, Value: nonce, Path: signinCallbackPath,
		MaxAge: stateMaxAge, HttpOnly: true, Secure: RequestScheme(r) == "https", SameSite: http.SameSiteLaxMode,
	})
	state := signState(signinState{nonce: nonce, next: next, retried: retried}, time.Now().Add(stateMaxAge*time.Second))

	q := url.Values{
		"client_id":    {g.clientID},
		"redirect_uri": {callbackURL(r)},
		// "repo" so GET /repos/{owner}/{repo} can see the user's PRIVATE repos
		"scope":        {"repo"},
		"state":        {state},
		"allow_signup": {"false"},
	}
	http.Redirect(w, r, githubAuthorizeURL+"?"+q.Encode(), http.StatusSeeOther)
}

// handleSigninCallback completes the OAuth round-trip. No failure here may be a
// dead end: the browser is sitting on the fixed callback URL, where a reload
// just re-submits the consumed single-use code, so every exit either restarts
// the flow or renders a page with a way to. And never with a 5xx -- Cloudflare
// replaces origin 5xx bodies with its own bare error page, which used to strand
func handleSigninCallback(w http.ResponseWriter, r *http.Request) {
	g := githubAuth()
	if g == nil {
		http.Error(w, "Sign in is not configured on this server.", http.StatusNotImplemented)
		return
	}
	q := r.URL.Query()
	st, expired, ok := parseState(q.Get("state"))
	if !ok {
		// Forged or corrupt state: nothing in it can be trusted, so no automatic
		slog.WarnContext(r.Context(), "github signin: invalid state at callback")
		signinFailedHTML(w, r, http.StatusBadRequest, "This sign-in link is invalid. It may have been truncated or altered.", "")
		return
	}
	// st is MAC-verified from here on (even when expired), so its next URL is
	// trustworthy -- and safeNextURL re-validates it at every use anyway.
	if e := q.Get("error"); e != "" {
		slog.WarnContext(r.Context(), "github signin: github returned an error", "error", e, "description", q.Get("error_description"))
		signinFailedHTML(w, r, http.StatusForbidden, "GitHub sign-in was cancelled or failed.", st.next)
		return
	}
	if expired {
		slog.InfoContext(r.Context(), "github signin: state expired", "retried", st.retried)
		restartOrFail(w, r, st, "The sign-in attempt took too long.")
		return
	}
	// Double-submit: the state's nonce must match the cookie set at start. A
	// mismatch is usually benign -- the cookie expired, or a newer sign-in tab
	// overwrote it -- so restart rather than reject.
	if c, err := r.Cookie(stateCookieName); err != nil || c.Value != st.nonce {
		slog.InfoContext(r.Context(), "github signin: state nonce does not match cookie", "retried", st.retried)
		restartOrFail(w, r, st, "Your browser did not present the sign-in cookie.")
		return
	}
	clearCookie(w, r, stateCookieName, signinCallbackPath)

	token, err := g.exchangeCode(r.Context(), q.Get("code"), callbackURL(r))
	if err != nil {
		slog.ErrorContext(r.Context(), "github signin: code exchange failed", "err", err)
		signinFailedHTML(w, r, http.StatusForbidden, "GitHub did not accept the sign-in.", st.next)
		return
	}
	login, err := g.fetchLogin(r.Context(), token)
	if err != nil {
		slog.ErrorContext(r.Context(), "github signin: fetching user identity failed", "err", err)
		signinFailedHTML(w, r, http.StatusForbidden, "Could not read your GitHub identity.", st.next)
		return
	}
	slog.InfoContext(r.Context(), "github signin: signed in", "login", login)
	// The session carries the user's login and token; per-resource authorization
	session := mintSession(login, token, time.Now().Add(sessionMaxAge*time.Second))
	setSessionCookie(w, r, session)
	dest := safeNextURL(r, st.next)
	// A site-domain destination cannot receive this apex's cookie (different
	if target := mintSiteHandoff(session, dest); target != "" {
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// restartOrFail handles a recoverable callback failure (expired state, nonce
// cookie lost or overwritten by a newer tab): it transparently sends the
func restartOrFail(w http.ResponseWriter, r *http.Request, st signinState, reason string) {
	if st.retried {
		signinFailedHTML(w, r, http.StatusBadRequest, reason, st.next)
		return
	}
	http.Redirect(w, r, apexRootURL(r)+signinStartPath+"?retry=1&next="+url.QueryEscape(st.next), http.StatusSeeOther)
}

func signinFailedHTML(w http.ResponseWriter, r *http.Request, status int, reason, next string) {
	retry := apexRootURL(r) + signinStartPath
	if next != "" {
		retry += "?next=" + url.QueryEscape(next)
	}
	signinFailedPage(w, r, status, reason, retry)
}

// signinFailedPage is the shared terminal failure page body, parameterized on
// the restart URL so the /__sso redemption (whose restart lives on the PRIMARY
// apex, not this request's own) can reuse it.
func signinFailedPage(w http.ResponseWriter, _ *http.Request, status int, reason, retry string) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	esc := template.HTMLEscapeString
	fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign-in failed</title>
<style>
  body { font-family: system-ui, -apple-system, sans-serif; max-width: 34rem; margin: 12vh auto; padding: 0 1.25rem; line-height: 1.55; }
  h1 { font-size: 1.4rem; margin-bottom: .5rem; }
  a.btn { display: inline-block; margin-top: 1rem; padding: .55rem .9rem; border: 1px solid; border-radius: .4rem; text-decoration: none; }
</style>
</head>
<body>
<h1>Sign-in failed</h1>
<p>%s</p>
<div><a class="btn" href="%s">Try signing in again</a></div>
</body>
</html>
`, esc(reason), esc(retry))
}

func handleSignout(w http.ResponseWriter, r *http.Request) {
	clearCookie(w, r, sessionCookieName, "/")
	http.Redirect(w, r, safeNextURL(r, r.URL.Query().Get("next")), http.StatusSeeOther)
}
