package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sign in with GitHub.
//
// buildhost serves both public and private content on the same hosts (a site
// branch is public or private per-row), so it cannot gate a whole service
// subdomain. Instead a browser that hits a *private* resource is redirected to
// GitHub's OAuth login; on return buildhost checks the user is a member of an
// allowed org and mints a session cookie. A signed-in human may READ private
// resources (org membership is the authorization gate); it never grants write.
//
// The OAuth callback is a single fixed URL (GitHub OAuth apps register one), so
// the whole flow runs on the apex and the session cookie is set domain-wide
// (Domain=<apex>) to work across every service subdomain.

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
	// Apex-only: the GitHub OAuth callback is one fixed URL, and the session
	// cookie is domain-wide, so the whole flow lives on the apex.
	HandleRaw("GET "+signinStartPath, handleSigninStart)
	HandleRaw("GET "+signinCallbackPath, handleSigninCallback)
	HandleRaw("GET "+signoutPath, handleSignout)
}

// GitHubAuth performs the OAuth Authorization Code flow against GitHub. A
// signed-in user is authorized for a private project by their access to that
// project's GitHub repo (no org allowlist). It is nil (disabled) unless a client
// id and secret are configured.
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

// repoCheckResult classifies one GET /repos/{owner}/{repo} access probe.
type repoCheckResult int

const (
	// repoCheckTransient is a non-answer: network error, 5xx, 429, or a 403
	// (rate limit / abuse detection). Deny the current request, cache nothing.
	repoCheckTransient repoCheckResult = iota
	// repoCheckAllowed: 200 -- the token's user can access the repo.
	repoCheckAllowed
	// repoCheckNoAccess: 404 -- definite no access (GitHub 404s a repo the
	// token cannot see rather than 403, so existence never leaks).
	repoCheckNoAccess
	// repoCheckTokenDead: 401 -- the credential itself is dead (revoked or
	// expired), not "no access to this repo". On this fixed-host authenticated
	// GET, GitHub reports rate limiting as 403/429 -- never 401 -- so a 401
	// unambiguously means the session's embedded token died mid-session.
	repoCheckTokenDead
)

// NewGitHubAuth returns a configured GitHubAuth, or nil if either the client id
// or secret is empty (the feature is then disabled and browsers fall back to the
// plain JSON 401).
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
// domain cannot run the OAuth flow on its own apex -- the single GitHub OAuth
// callback is registered on the primary apex -- so it is sent there instead and
// /__sso hands the session back afterward. Returns "" when that hop is
// unavailable (site-domain host, no BUILDHOST_PRIMARY_DOMAIN configured); the
// caller then falls back to the plain JSON 401.
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
// which (now anonymous) sends it to GitHub sign-in -- so a forbidden user can
// re-authenticate as a different account.
func signoutURL(r *http.Request) string {
	next := RequestBaseURL(r) + r.URL.RequestURI()
	return apexRootURL(r) + signoutPath + "?next=" + url.QueryEscape(next)
}

// apexRootURL returns scheme://<apex>, deriving the apex from the request Host by
// stripping a known leading service label (apt/dl/sites/...). Correct whether
// called from a service subdomain (strips it) or the apex itself (nothing to
// strip) -- unlike RequestRootURL, which strips the first label unconditionally.
// A host on the configured site domain classifies to the SITE apex (myapp.pazer.site
// -> pazer.site): it is a different registrable domain, never the primary apex.
func apexRootURL(r *http.Request) string {
	host, port := r.Host, ""
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host, port = host[:i], host[i:]
	}
	if sd := siteApexOf(host); sd != "" {
		host = sd
	} else if dot := strings.IndexByte(host, '.'); dot > 0 && knownServiceLabels[host[:dot]] {
		host = host[dot+1:]
	}
	return RequestScheme(r) + "://" + host + port
}

// callbackURL is the fixed redirect_uri registered with the GitHub OAuth app:
// scheme://<apex>/__signin/callback. The sign-in routes only run on the apex
// (HandleRaw), so r.Host is already the apex -- use RequestBaseURL (scheme +
// Host) directly; RequestRootURL would wrongly strip the apex's first label.
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
	// can be dead (revoked or expired mid-session), and handing a dead session
	// across would loop -- the site domain's dead-session re-auth
	// (unauthorizedResponse) bounces the browser back here, where the still-MAC-
	// valid apex cookie would mint the same dead session again, forever. So the
	// token is probed first (the same GET /user the OAuth callback trusts before
	// minting); a dead one falls through to the full OAuth flow below, which
	// replaces the apex session and hands a FRESH one across.
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
	// retry=1 marks a flow the callback auto-restarted after a recoverable
	// failure. The marker rides the signed state, so if the restarted flow fails
	// again the callback renders a terminal page instead of redirecting forever.
	// It never relaxes any validation.
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
		// (the only classic OAuth scope that grants private-repo visibility).
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
// users on "error code: 502" with no explanation and nothing logged.
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
		// redirect -- just a page offering a fresh start.
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
	// is the user's access to that project's repo, checked at request time.
	session := mintSession(login, token, time.Now().Add(sessionMaxAge*time.Second))
	setSessionCookie(w, r, session)
	dest := safeNextURL(r, st.next)
	// A site-domain destination cannot receive this apex's cookie (different
	// registrable domain): park the session and send the browser through the
	// site domain's /__sso, which sets it there. The session value itself never
	// rides the URL -- only a signed one-time code.
	if target := mintSiteHandoff(session, dest); target != "" {
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// restartOrFail handles a recoverable callback failure (expired state, nonce
// cookie lost or overwritten by a newer tab): it transparently sends the
// browser back through /__signin to try again -- once. The restarted flow's
// state carries the retried marker, so a second failure renders the terminal
// page instead of looping.
func restartOrFail(w http.ResponseWriter, r *http.Request, st signinState, reason string) {
	if st.retried {
		signinFailedHTML(w, r, http.StatusBadRequest, reason, st.next)
		return
	}
	http.Redirect(w, r, apexRootURL(r)+signinStartPath+"?retry=1&next="+url.QueryEscape(st.next), http.StatusSeeOther)
}

// signinFailedHTML renders a terminal sign-in failure: one plain sentence about
// what went wrong plus a link to start over (the browser is parked on the
// callback URL, where reloading can never succeed -- the code is single-use).
// Always 4xx, never 5xx, so Cloudflare passes the page through instead of
// substituting its own.
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
	// Relax the global default-src 'none' just enough for the one inline <style>;
	// no scripts, no external resources (same approach as signedInForbiddenHTML).
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

// --- GitHub API ---

// exchangeCode trades an authorization code for a user access token.
func (g *GitHubAuth) exchangeCode(ctx context.Context, code, redirectURI string) (string, error) {
	if code == "" {
		return "", fmt.Errorf("missing code")
	}
	form := url.Values{
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", githubTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := g.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	// GitHub reports exchange failures (bad_verification_code,
	// incorrect_client_credentials, redirect_uri_mismatch, ...) as an error JSON
	// body, typically under HTTP 200 -- so classify by body, not status, and
	// carry GitHub's own diagnosis in the error (it names no secrets or codes).
	var body struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("token endpoint HTTP %d: %w", resp.StatusCode, err)
	}
	if body.AccessToken == "" {
		if body.Error == "" {
			return "", fmt.Errorf("token endpoint HTTP %d: no access token in response", resp.StatusCode)
		}
		return "", fmt.Errorf("token endpoint HTTP %d: %s: %s", resp.StatusCode, body.Error, body.ErrorDesc)
	}
	return body.AccessToken, nil
}

// fetchLogin returns the authenticated user's GitHub login (GET /user).
func (g *GitHubAuth) fetchLogin(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", githubAPIBase+"/user", nil)
	if err != nil {
		return "", err
	}
	setGitHubHeaders(req, token)
	resp, err := g.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET /user -> %d", resp.StatusCode)
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&user); err != nil || user.Login == "" {
		return "", fmt.Errorf("no login in /user response")
	}
	return user.Login, nil
}

// canAccessRepo reports whether the signed-in user (identified by login, using
// their token) can access ownerRepo -- i.e. GET /repos/{owner}/{repo} returns
// 200 -- and, separately, whether the probe found the token itself dead (401:
// revoked or expired mid-session), so the caller can re-auth the browser
// instead of telling the user their account lacks access. Results are cached
// per (login, repo, token) for a short TTL so the GitHub call does not run on
// every asset request.
//
// Two properties keep the cache from ever locking out a user who DOES have
// access (the failure that made an authorized repo owner see "Access denied"):
//
//   - Only an *authoritative* answer is cached -- a definite 200 (access), 404
//     (no access / not visible to this token), or 401 (the token itself is
//     dead; dead tokens never come back, and the fingerprint key below means a
//     fresh sign-in is re-checked, so caching it only spares GitHub the
//     re-probes while the browser follows the re-auth redirect). A transient
//     failure (network error, 5xx, 429, or a rate-limit 403) denies only the
//     current request and is NOT cached, so a refresh re-checks immediately
//     instead of being pinned for the whole TTL on one momentary GitHub hiccup.
//   - The cache key includes a fingerprint of the token, so a user who re-signs
//     in with a fresh, broader-scoped token is never shadowed by a negative
//     cached against their previous token.
func (g *GitHubAuth) canAccessRepo(ctx context.Context, login, token, ownerRepo string) (allowed, tokenDead bool) {
	if login == "" || token == "" || !validRepoPath(ownerRepo) {
		return false, false
	}
	key := login + "\x00" + ownerRepo + "\x00" + tokenFingerprint(token)
	now := time.Now()
	g.mu.Lock()
	if e, ok := g.repoCache[key]; ok && now.Before(e.exp) {
		g.mu.Unlock()
		return e.result == repoCheckAllowed, e.result == repoCheckTokenDead
	}
	g.mu.Unlock()

	result := g.checkRepoAccess(ctx, token, ownerRepo)
	if result == repoCheckTransient {
		// Non-answer (transient error / rate limit): fail closed for this request
		// but do not cache, so the next request re-checks rather than inheriting a
		// stale denial.
		return false, false
	}

	g.mu.Lock()
	g.repoCache[key] = repoAccess{result: result, exp: now.Add(repoAccessTTL)}
	g.mu.Unlock()
	return result == repoCheckAllowed, result == repoCheckTokenDead
}

// checkRepoAccess performs GET /repos/{owner}/{repo} and classifies the result
// three ways: 200 = access, 404 = definite no access (GitHub 404s a repo the
// token cannot see, rather than 403, so existence never leaks), 401 = the token
// itself is dead (revoked or expired -- on this endpoint rate limiting is
// 403/429, never 401). Anything else (network error, 5xx, 429, or a rate-limit
// 403) is transient -- a non-answer the caller must never cache as a hard
// denial, and must NOT treat as token-dead (that would kick a signed-in user
// through a pointless re-auth on every GitHub hiccup).
func (g *GitHubAuth) checkRepoAccess(ctx context.Context, token, ownerRepo string) repoCheckResult {
	req, err := http.NewRequestWithContext(ctx, "GET", githubAPIBase+"/repos/"+ownerRepo, nil)
	if err != nil {
		return repoCheckTransient
	}
	setGitHubHeaders(req, token)
	resp, err := g.http.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "github repo-access check failed", "repo", ownerRepo, "err", err)
		return repoCheckTransient
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	switch resp.StatusCode {
	case http.StatusOK:
		return repoCheckAllowed
	case http.StatusNotFound:
		return repoCheckNoAccess
	case http.StatusUnauthorized:
		slog.WarnContext(ctx, "github repo-access check: token rejected, session token is dead", "repo", ownerRepo, "status", resp.StatusCode)
		return repoCheckTokenDead
	default:
		slog.WarnContext(ctx, "github repo-access check: transient failure, denying without caching", "repo", ownerRepo, "status", resp.StatusCode)
		return repoCheckTransient
	}
}

// tokenFingerprint returns a short, non-reversible fingerprint of a token, used
// only as part of the in-memory repo-access cache key so the raw token is never
// held as a map key while two distinct tokens still hash to distinct keys.
func tokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

func setGitHubHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "buildhost")
}

// --- signed session + state (HMAC over the shared signing key) ---

// mintSession signs the user's login + GitHub token into the session value. The
// token is needed at request time to check the user's access to a project's repo.
func mintSession(login, token string, exp time.Time) string {
	return signValue("session", login+"\x00"+token, exp)
}

func verifySession(value string) (login, token string, ok bool) {
	payload, valid := verifySignedValue("session", value)
	if !valid {
		return "", "", false
	}
	l, t, found := strings.Cut(payload, "\x00")
	if !found {
		return "", "", false
	}
	return l, t, true
}

// signinState is the payload bound into the signed OAuth state parameter: the
// CSRF nonce, the URL to return to after sign-in, and whether this flow is
// already an automatic retry (so a failing flow restarts at most once).
type signinState struct {
	nonce   string
	next    string
	retried bool
}

func signState(st signinState, exp time.Time) string {
	flags := ""
	if st.retried {
		flags = "r"
	}
	return signValue("state", st.nonce+"\x00"+flags+"\x00"+st.next, exp)
}

// parseState authenticates an OAuth state parameter. ok reports a valid
// signature; everything in st is then trustworthy even when expired is set
// (the MAC covers the expiry too), so an expired flow can still be restarted
// to its own next URL. ok=false means forged or corrupt: use nothing from it.
func parseState(value string) (st signinState, expired, ok bool) {
	payload, expired, ok := parseSignedValue("state", value)
	if !ok {
		return signinState{}, false, false
	}
	nonce, rest, found := strings.Cut(payload, "\x00")
	if !found {
		return signinState{}, false, false
	}
	flags, next, found := strings.Cut(rest, "\x00")
	if !found {
		// A state minted before the retried flag existed (nonce\x00next): still
		// authentic; treat as a first attempt.
		return signinState{nonce: nonce, next: rest}, expired, true
	}
	return signinState{nonce: nonce, next: next, retried: flags == "r"}, expired, true
}

// signValue returns base64(payload).base64(mac) where mac is HMAC over the
// domain-separated (purpose, payload, exp). verifySignedValue checks the mac and
// expiry and returns the payload.
func signValue(purpose, payload string, exp time.Time) string {
	body := payload + "\x00" + strconv.FormatInt(exp.Unix(), 10)
	mac := valueMAC(purpose, body)
	return base64.RawURLEncoding.EncodeToString([]byte(body)) + "." + base64.RawURLEncoding.EncodeToString(mac)
}

func verifySignedValue(purpose, value string) (string, bool) {
	payload, expired, ok := parseSignedValue(purpose, value)
	return payload, ok && !expired
}

// parseSignedValue verifies the MAC and splits off the expiry. ok reports an
// authentic (correctly signed) value; expired is reported separately so a
// caller can distinguish "forged" from "genuine but past its expiry".
func parseSignedValue(purpose, value string) (payload string, expired, ok bool) {
	dot := strings.IndexByte(value, '.')
	if dot <= 0 {
		return "", false, false
	}
	body, err := base64.RawURLEncoding.DecodeString(value[:dot])
	if err != nil {
		return "", false, false
	}
	gotMAC, err := base64.RawURLEncoding.DecodeString(value[dot+1:])
	if err != nil || !hmac.Equal(gotMAC, valueMAC(purpose, string(body))) {
		return "", false, false
	}
	payload, expStr, ok := cutLast(string(body), '\x00')
	if !ok {
		return "", false, false
	}
	expUnix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", false, false
	}
	return payload, time.Now().Unix() > expUnix, true
}

// cutLast splits on the final occurrence of sep, so a payload that itself
// contains sep (state's nonce\x00next) round-trips with the exp suffix.
func cutLast(s string, sep byte) (before, after string, found bool) {
	i := strings.LastIndexByte(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}

func valueMAC(purpose, body string) []byte {
	h := hmac.New(sha256.New, downloadSecretBytes())
	h.Write([]byte("ghlogin:" + purpose + "\x00"))
	h.Write([]byte(body))
	return h.Sum(nil)
}

func randToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// --- cookies ---

func sessionFromRequest(r *http.Request) (login, token string, ok bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", "", false
	}
	return verifySession(c.Value)
}

// setSessionCookie sets the session domain-wide (Domain=<apex>) so a login on
// the apex callback authenticates the user on every service subdomain.
func setSessionCookie(w http.ResponseWriter, r *http.Request, value string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: value, Path: "/", Domain: apexHost(r),
		MaxAge: sessionMaxAge, HttpOnly: true, Secure: RequestScheme(r) == "https", SameSite: http.SameSiteLaxMode,
	})
}

func clearCookie(w http.ResponseWriter, r *http.Request, name, path string) {
	domain := ""
	if name == sessionCookieName {
		domain = apexHost(r)
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: path, Domain: domain,
		MaxAge: -1, HttpOnly: true, Secure: RequestScheme(r) == "https", SameSite: http.SameSiteLaxMode,
	})
}

// apexHost is the registrable host the session cookie is scoped to: the request
// Host minus port, with a known leading service label (sites/dl/...) stripped --
// the same apex derivation as apexRootURL. On the apex, where /__signin and the
// callback run, stripping is a no-op; it matters when the dead-session re-auth
// (unauthorizedResponse) clears the cookie from a service subdomain, since a
// Set-Cookie only removes the domain-wide cookie if its Domain matches the one
// the cookie was set with. A host on the configured site domain classifies to
// the SITE apex (myapp.pazer.site -> pazer.site) -- a different registrable
// domain -- so a cookie minted by the /__sso redemption covers every project
// site under the domain, and a clear from a site host removes the site-domain
// cookie.
func apexHost(r *http.Request) string {
	host := hostNoPort(r.Host)
	if sd := siteApexOf(host); sd != "" {
		return sd
	}
	if dot := strings.IndexByte(host, '.'); dot > 0 && knownServiceLabels[host[:dot]] {
		host = host[dot+1:]
	}
	return host
}

// safeNextURL keeps post-login redirects inside this deployment: it accepts an
// absolute URL only if its host is the apex or one of its subdomains, and falls
// back to the apex root otherwise -- so the flow can't be turned into an open
// redirect. A relative path (leading "/") is also accepted.
func safeNextURL(r *http.Request, next string) string {
	// Sign-in runs on the apex, so the request Host is the apex root.
	root := RequestBaseURL(r)
	if next == "" {
		return root + "/"
	}
	if next[0] == '/' && !strings.HasPrefix(next, "//") && !strings.HasPrefix(next, "/\\") {
		return next
	}
	u, err := url.Parse(next)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return root + "/"
	}
	apex := apexHost(r)
	host := u.Hostname()
	if host == apex || strings.HasSuffix(host, "."+apex) {
		return next
	}
	// The configured site domain (or exactly one label under it) is part of this
	// deployment too: the cross-domain handoff signs a browser in on the primary
	// apex and returns it to a project site. Anything else is still rejected --
	// notably lookalikes like x.<site-domain>.evil.com, which fail siteApexOf's
	// exact-suffix check.
	if siteApexOf(host) != "" {
		return next
	}
	return root + "/"
}
