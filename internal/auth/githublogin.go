package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

// GitHubAuth performs the OAuth Authorization Code flow against GitHub and gates
// access on membership of an allowed org. It is nil (disabled) unless a client
// id, secret, and at least one org are configured.
type GitHubAuth struct {
	clientID     string
	clientSecret string
	allowedOrgs  []string
	http         *http.Client
}

// NewGitHubAuth returns a configured GitHubAuth, or nil if any of client id,
// secret, or the org allowlist is empty (the feature is then disabled and
// browsers fall back to the plain JSON 401).
func NewGitHubAuth(clientID, clientSecret string, allowedOrgs []string) *GitHubAuth {
	clientID = strings.TrimSpace(clientID)
	clientSecret = strings.TrimSpace(clientSecret)
	var orgs []string
	for _, o := range allowedOrgs {
		if o = strings.TrimSpace(o); o != "" {
			orgs = append(orgs, o)
		}
	}
	if clientID == "" || clientSecret == "" || len(orgs) == 0 {
		return nil
	}
	return &GitHubAuth{
		clientID:     clientID,
		clientSecret: clientSecret,
		allowedOrgs:  orgs,
		http:         &http.Client{Timeout: 15 * time.Second},
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
// URL (which may be on a service subdomain).
func loginRedirectURL(r *http.Request) string {
	next := RequestBaseURL(r) + r.URL.RequestURI()
	return apexRootURL(r) + signinStartPath + "?next=" + url.QueryEscape(next)
}

// apexRootURL returns scheme://<apex>, deriving the apex from the request Host by
// stripping a known leading service label (apt/dl/sites/...). Correct whether
// called from a service subdomain (strips it) or the apex itself (nothing to
// strip) -- unlike RequestRootURL, which strips the first label unconditionally.
func apexRootURL(r *http.Request) string {
	host, port := r.Host, ""
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host, port = host[:i], host[i:]
	}
	if dot := strings.IndexByte(host, '.'); dot > 0 && knownServiceLabels[host[:dot]] {
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

	nonce := randToken()
	// Bind the destination into a signed state and tie the flow to this browser
	// via a short-lived cookie (double-submit), so the callback can't be forged
	// or pointed elsewhere.
	http.SetCookie(w, &http.Cookie{
		Name: stateCookieName, Value: nonce, Path: signinCallbackPath,
		MaxAge: stateMaxAge, HttpOnly: true, Secure: RequestScheme(r) == "https", SameSite: http.SameSiteLaxMode,
	})
	state := signState(nonce, next, time.Now().Add(stateMaxAge*time.Second))

	q := url.Values{
		"client_id":    {g.clientID},
		"redirect_uri": {callbackURL(r)},
		"scope":        {"read:org"},
		"state":        {state},
		"allow_signup": {"false"},
	}
	http.Redirect(w, r, githubAuthorizeURL+"?"+q.Encode(), http.StatusSeeOther)
}

func handleSigninCallback(w http.ResponseWriter, r *http.Request) {
	g := githubAuth()
	if g == nil {
		http.Error(w, "Sign in is not configured on this server.", http.StatusNotImplemented)
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		http.Error(w, "GitHub sign-in was cancelled or failed.", http.StatusForbidden)
		return
	}
	nonce, next, ok := verifyState(q.Get("state"))
	if !ok {
		http.Error(w, "Invalid or expired sign-in state.", http.StatusBadRequest)
		return
	}
	// Double-submit: the state's nonce must match the cookie set at start.
	if c, err := r.Cookie(stateCookieName); err != nil || c.Value != nonce {
		http.Error(w, "Sign-in state mismatch.", http.StatusBadRequest)
		return
	}
	clearCookie(w, r, stateCookieName, signinCallbackPath)

	token, err := g.exchangeCode(r.Context(), q.Get("code"), callbackURL(r))
	if err != nil {
		http.Error(w, "Could not complete GitHub sign-in.", http.StatusBadGateway)
		return
	}
	login, err := g.authorizedLogin(r.Context(), token)
	if err != nil {
		// Authenticated with GitHub but not a member of an allowed org.
		http.Error(w, "Your GitHub account is not authorized to view private resources here.", http.StatusForbidden)
		return
	}
	setSessionCookie(w, r, mintSession(login, time.Now().Add(sessionMaxAge*time.Second)))
	http.Redirect(w, r, safeNextURL(r, next), http.StatusSeeOther)
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
	var body struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("no access token (%s)", body.Error)
	}
	return body.AccessToken, nil
}

// authorizedLogin returns the user's login if they are a member of any allowed
// org, else an error. Identity comes from GET /user; membership from
// GET /user/memberships/orgs/{org} (requires the read:org scope).
func (g *GitHubAuth) authorizedLogin(ctx context.Context, token string) (string, error) {
	var user struct {
		Login string `json:"login"`
	}
	if err := g.apiGet(ctx, token, "/user", &user); err != nil || user.Login == "" {
		return "", fmt.Errorf("identify user: %w", err)
	}
	for _, org := range g.allowedOrgs {
		var m struct {
			State string `json:"state"`
		}
		if err := g.apiGet(ctx, token, "/user/memberships/orgs/"+url.PathEscape(org), &m); err == nil && m.State == "active" {
			return user.Login, nil
		}
	}
	return "", fmt.Errorf("user %q not in an allowed org", user.Login)
}

func (g *GitHubAuth) apiGet(ctx context.Context, token, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", githubAPIBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "buildhost")
	resp, err := g.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub %s -> %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// --- signed session + state (HMAC over the shared signing key) ---

func mintSession(login string, exp time.Time) string { return signValue("session", login, exp) }

func verifySession(value string) (string, bool) { return verifySignedValue("session", value) }

func signState(nonce, next string, exp time.Time) string {
	return signValue("state", nonce+"\x00"+next, exp)
}

func verifyState(value string) (nonce, next string, ok bool) {
	payload, valid := verifySignedValue("state", value)
	if !valid {
		return "", "", false
	}
	n, nx, found := strings.Cut(payload, "\x00")
	if !found {
		return "", "", false
	}
	return n, nx, true
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
	dot := strings.IndexByte(value, '.')
	if dot <= 0 {
		return "", false
	}
	body, err := base64.RawURLEncoding.DecodeString(value[:dot])
	if err != nil {
		return "", false
	}
	gotMAC, err := base64.RawURLEncoding.DecodeString(value[dot+1:])
	if err != nil || !hmac.Equal(gotMAC, valueMAC(purpose, string(body))) {
		return "", false
	}
	payload, expStr, ok := cutLast(string(body), '\x00')
	if !ok {
		return "", false
	}
	expUnix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > expUnix {
		return "", false
	}
	return payload, true
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

func sessionFromRequest(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
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

// apexHost is the registrable host the session cookie is scoped to (request Host
// minus port). /__signin runs on the apex, so r.Host is already the apex there.
func apexHost(r *http.Request) string {
	return hostNoPort(r.Host)
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
	return root + "/"
}
