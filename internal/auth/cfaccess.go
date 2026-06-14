package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Cloudflare Access integration.
//
// buildhost serves both public and private content on the same hosts (a site
// branch is public or private per-row), so it cannot put Cloudflare Access in
// front of a whole service subdomain without gating the anonymous public
// previews too. Instead a browser that hits a *private* resource is redirected
// to /__access -- a single path the operator protects with a Cloudflare Access
// self-hosted application. Cloudflare runs its hosted login there and forwards
// the request to buildhost with a signed `Cf-Access-Jwt-Assertion`. buildhost
// verifies that JWT (against the team's JWKS) once, then mints its own short
// session cookie so the actual content paths -- which are NOT behind Access --
// recognize the signed-in human without depending on Cloudflare forwarding the
// header on every path. A valid Cloudflare Access session grants READ to private
// resources (the Access policy is the authorization gate); it never grants write.

const (
	cfAccessCallbackPath = "/__access"
	// cfAccessHeader is the assertion Cloudflare injects on requests to an
	// Access-protected path. Preferred over the CF_Authorization cookie, which
	// is not guaranteed to be forwarded.
	cfAccessHeader = "Cf-Access-Jwt-Assertion"

	cfSessionCookieName = "bh_cfaccess"
	cfSessionMaxAge     = 12 * 60 * 60 // 12h; the JWT's own exp still bounds login.
)

// cfAccessSubdomains mirrors the service hosts; /__access is registered on each
// (plus the apex) so the same-host redirect resolves wherever the gated resource
// lives. The router's strict host partitioning means an apex-only registration
// would 404 on a subdomain.
var cfAccessSubdomains = []string{"apt", "brew", "dl", "git", "npm", "oci", "sites", "static"}

func init() {
	HandleRaw("GET "+cfAccessCallbackPath, handleCFAccessCallback)
	for _, svc := range cfAccessSubdomains {
		ServiceHandleRaw(svc, "GET "+cfAccessCallbackPath, handleCFAccessCallback)
	}
}

// CFAccessVerifier validates Cloudflare Access JWTs for one self-hosted
// application: signature against the team JWKS, issuer == team domain, audience
// == the application's AUD tag, and expiry. It is nil (disabled) unless both the
// team domain and AUD are configured.
type CFAccessVerifier struct {
	teamDomain string // e.g. https://<team>.cloudflareaccess.com
	aud        string

	mu       sync.Mutex
	keys     []jwkKey
	keysExp  time.Time
	keysHTTP *http.Client
}

// NewCFAccessVerifier returns a verifier, or nil if either value is empty (the
// feature is then disabled and buildhost falls back to the plain JSON 401).
func NewCFAccessVerifier(teamDomain, aud string) *CFAccessVerifier {
	teamDomain = strings.TrimRight(strings.TrimSpace(teamDomain), "/")
	aud = strings.TrimSpace(aud)
	if teamDomain == "" || aud == "" {
		return nil
	}
	return &CFAccessVerifier{
		teamDomain: teamDomain,
		aud:        aud,
		keysHTTP:   &http.Client{Timeout: 10 * time.Second},
	}
}

type cfAccessClaims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

// Verify checks an Access JWT and returns the authenticated email on success.
func (v *CFAccessVerifier) Verify(ctx context.Context, raw string) (string, error) {
	keys, err := v.getKeys(ctx)
	if err != nil {
		return "", fmt.Errorf("fetch Access certs: %w", err)
	}
	claims := &cfAccessClaims{}
	_, err = jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unsupported algorithm: %v", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		for _, key := range keys {
			if kid == "" || key.Kid == kid {
				return key.Pub, nil
			}
		}
		return nil, errors.New("no matching key found")
	}, jwt.WithIssuer(v.teamDomain), jwt.WithAudience(v.aud), jwt.WithExpirationRequired())
	if err != nil {
		return "", fmt.Errorf("verify Access token: %w", err)
	}
	return claims.Email, nil
}

func (v *CFAccessVerifier) getKeys(ctx context.Context) ([]jwkKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if time.Now().Before(v.keysExp) && len(v.keys) > 0 {
		return v.keys, nil
	}
	keys, err := v.fetchCerts(ctx)
	if err != nil {
		if len(v.keys) > 0 {
			return v.keys, nil // serve stale keys rather than fail on a transient fetch error
		}
		return nil, err
	}
	v.keys = keys
	v.keysExp = time.Now().Add(10 * time.Minute)
	return keys, nil
}

// fetchCerts loads the team's signing keys from the fixed Access certs endpoint
// (<team>/cdn-cgi/access/certs) -- not OIDC discovery, which Access does not use.
func (v *CFAccessVerifier) fetchCerts(ctx context.Context) ([]jwkKey, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", v.teamDomain+"/cdn-cgi/access/certs", nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.keysHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("certs endpoint returned %d", resp.StatusCode)
	}
	var raw struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	var keys []jwkKey
	for _, rawKey := range raw.Keys {
		var k struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		}
		if err := json.Unmarshal(rawKey, &k); err != nil || k.Kty != "RSA" {
			continue
		}
		pub, err := parseRSAPublicKey(k.N, k.E)
		if err != nil {
			continue
		}
		keys = append(keys, jwkKey{Kid: k.Kid, Pub: pub})
	}
	if len(keys) == 0 {
		return nil, errors.New("no usable RSA keys in Access certs")
	}
	return keys, nil
}

// handleCFAccessCallback is the endpoint the operator protects with a Cloudflare
// Access self-hosted application. Cloudflare authenticates the human and forwards
// here with a Cf-Access-Jwt-Assertion; buildhost verifies it, mints a session
// cookie, and sends the browser back to the resource it originally wanted.
func handleCFAccessCallback(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	v := cfAccessVerifier()
	if v == nil {
		cfAccessError(w, http.StatusNotImplemented, "Cloudflare Access sign-in is not configured on this server.")
		return
	}
	assertion := r.Header.Get(cfAccessHeader)
	if assertion == "" {
		// Reached buildhost without Cloudflare's assertion -- the path is not
		// actually behind a Cloudflare Access application. Do NOT redirect (that
		// would loop); tell the operator what to fix.
		cfAccessError(w, http.StatusUnauthorized, "No Cloudflare Access assertion was present. This path must be protected by a Cloudflare Access application.")
		return
	}
	email, err := v.Verify(r.Context(), assertion)
	if err != nil {
		cfAccessError(w, http.StatusForbidden, "Cloudflare Access verification failed.")
		return
	}
	setCFSessionCookie(w, r, mintCFSession(email, time.Now().Add(cfSessionMaxAge*time.Second)))
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func cfAccessError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write([]byte(msg + "\n"))
}

// cfAccessVerifier returns the configured verifier (nil if disabled).
func cfAccessVerifier() *CFAccessVerifier {
	if mw == nil {
		return nil
	}
	return mw.CFAccess
}

// cfAccessEnabled reports whether the Cloudflare Access sign-in flow is active.
func cfAccessEnabled() bool { return cfAccessVerifier() != nil }

// loginRedirectURL is where a browser that needs to authenticate is sent: the
// Access-protected callback, carrying a next= back to the original resource.
func loginRedirectURL(r *http.Request) string {
	return cfAccessCallbackPath + "?next=" + url.QueryEscape(r.URL.RequestURI())
}

// --- buildhost session cookie (proof of a verified Cloudflare Access login) ---

// mintCFSession returns a buildhost-signed value asserting email is authenticated
// until exp. Signed with the shared signing key (downloadSecretBytes), so it
// needs no extra key material and survives restarts.
func mintCFSession(email string, exp time.Time) string {
	payload := email + "\x00" + strconv.FormatInt(exp.Unix(), 10)
	mac := cfSessionMAC(payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac)
}

// verifyCFSession returns the email if value is a valid, unexpired session.
func verifyCFSession(value string) (string, bool) {
	dot := strings.IndexByte(value, '.')
	if dot <= 0 {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(value[:dot])
	if err != nil {
		return "", false
	}
	gotMAC, err := base64.RawURLEncoding.DecodeString(value[dot+1:])
	if err != nil {
		return "", false
	}
	if !hmac.Equal(gotMAC, cfSessionMAC(string(payload))) {
		return "", false
	}
	email, expStr, ok := strings.Cut(string(payload), "\x00")
	if !ok {
		return "", false
	}
	expUnix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > expUnix {
		return "", false
	}
	return email, true
}

func cfSessionMAC(payload string) []byte {
	h := hmac.New(sha256.New, downloadSecretBytes())
	h.Write([]byte("cfaccess\x00"))
	h.Write([]byte(payload))
	return h.Sum(nil)
}

// verifyCFSessionCookie reads and verifies the session cookie on a request.
func verifyCFSessionCookie(r *http.Request) (string, bool) {
	c, err := r.Cookie(cfSessionCookieName)
	if err != nil {
		return "", false
	}
	return verifyCFSession(c.Value)
}

func setCFSessionCookie(w http.ResponseWriter, r *http.Request, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cfSessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   cfSessionMaxAge,
		HttpOnly: true,
		Secure:   RequestScheme(r) == "https",
		SameSite: http.SameSiteLaxMode,
	})
}

// safeNext keeps post-login redirects same-site: only an absolute path is
// allowed, rejecting absolute URLs and scheme-relative ("//evil.com") targets so
// the callback can't be turned into an open redirect.
func safeNext(next string) string {
	if next == "" || next[0] != '/' || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}
