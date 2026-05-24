package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

var ErrOIDCNotMatched = errors.New("no matching OIDC policy")

type OIDCVerifier struct {
	mu             sync.RWMutex
	cache          map[string]*cachedJWKS
	trustedIssuers []string
	allowedOrgs    []string
	allowedEvents  []string
}

type cachedJWKS struct {
	keys   []jwkKey
	expiry time.Time
}

type jwkKey struct {
	Kid string
	Pub *rsa.PublicKey
}

type oidcClaims struct {
	jwt.RegisteredClaims
	EventName            string `json:"event_name"`
	RepositoryVisibility string `json:"repository_visibility"`
}

const oidcLeeway = 60 * time.Second

func NewOIDCVerifier(trustedIssuers, allowedOrgs, allowedEvents []string) *OIDCVerifier {
	return &OIDCVerifier{cache: make(map[string]*cachedJWKS), trustedIssuers: trustedIssuers, allowedOrgs: allowedOrgs, allowedEvents: allowedEvents}
}

func LooksLikeJWT(token string) bool {
	parts := strings.Split(token, ".")
	return len(parts) == 3 && len(token) > 100
}

// VerifyResult holds the result of OIDC verification beyond the token itself.
type VerifyResult struct {
	OIDCPrivate bool
}

func (v *OIDCVerifier) VerifyToken(ctx context.Context, raw string, policies []db.OIDCPolicy) (*db.APIToken, string, error) {
	return v.verifyTokenFull(ctx, raw, policies, nil)
}

func (v *OIDCVerifier) VerifyTokenFull(ctx context.Context, raw string, policies []db.OIDCPolicy, result *VerifyResult) (*db.APIToken, string, error) {
	return v.verifyTokenFull(ctx, raw, policies, result)
}

func (v *OIDCVerifier) verifyTokenFull(ctx context.Context, raw string, policies []db.OIDCPolicy, result *VerifyResult) (*db.APIToken, string, error) {
	unverified := &oidcClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(raw, unverified)
	if err != nil {
		return nil, "", fmt.Errorf("parse token: %w", err)
	}

	if unverified.ExpiresAt == nil {
		return nil, "", errors.New("token missing exp claim")
	}
	now := time.Now()
	if now.After(unverified.ExpiresAt.Time.Add(oidcLeeway)) {
		return nil, "", errors.New("token expired")
	}
	if unverified.NotBefore != nil && now.Before(unverified.NotBefore.Time.Add(-oidcLeeway)) {
		return nil, "", errors.New("token not yet valid")
	}

	var matchedPolicy *db.OIDCPolicy
	for i := range policies {
		p := &policies[i]
		if p.Issuer != unverified.Issuer {
			continue
		}
		if matchSubject(p.SubjectPattern, unverified.Subject) {
			matchedPolicy = p
			break
		}
	}
	if matchedPolicy != nil && matchedPolicy.Audience != "" {
		aud, _ := unverified.GetAudience()
		if !slices.Contains(aud, matchedPolicy.Audience) {
			return nil, "", errors.New("token audience does not match policy")
		}
	}

	if matchedPolicy == nil && !slices.Contains(v.trustedIssuers, unverified.Issuer) {
		return nil, "", ErrOIDCNotMatched
	}

	keys, err := v.getKeys(ctx, unverified.Issuer)
	if err != nil {
		return nil, "", fmt.Errorf("fetch JWKS: %w", err)
	}

	token, err := jwt.ParseWithClaims(raw, &oidcClaims{}, func(t *jwt.Token) (any, error) {
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
	}, jwt.WithLeeway(oidcLeeway), jwt.WithExpirationRequired())
	if err != nil {
		return nil, "", fmt.Errorf("verify token: %w", err)
	}

	verified := token.Claims.(*oidcClaims)

	if matchedPolicy != nil {
		return &db.APIToken{
			ID:        -1,
			Name:      "oidc:" + verified.Subject,
			ProjectID: matchedPolicy.ProjectID,
			Scopes:    matchedPolicy.Scopes,
		}, "", nil
	}

	org := orgFromSubject(verified.Subject)
	if !slices.Contains(v.allowedOrgs, "*") && !slices.Contains(v.allowedOrgs, org) {
		return nil, "", fmt.Errorf("org %q not in allowed list", org)
	}

	if verified.EventName != "" && !slices.Contains(v.allowedEvents, "*") && !slices.Contains(v.allowedEvents, verified.EventName) {
		return nil, "", fmt.Errorf("event %q not in allowed list", verified.EventName)
	}

	project := projectFromSubject(verified.Subject)
	if project == "" {
		return nil, "", errors.New("cannot derive project name from OIDC subject")
	}
	if result != nil {
		result.OIDCPrivate = verified.RepositoryVisibility != "public"
	}
	return &db.APIToken{
		ID:     -1,
		Name:   "oidc:" + verified.Subject,
		Scopes: "read,write",
	}, project, nil
}

func (v *OIDCVerifier) getKeys(ctx context.Context, issuer string) ([]jwkKey, error) {
	v.mu.RLock()
	if c, ok := v.cache[issuer]; ok && time.Now().Before(c.expiry) {
		keys := c.keys
		v.mu.RUnlock()
		return keys, nil
	}
	v.mu.RUnlock()

	v.mu.Lock()
	defer v.mu.Unlock()

	if c, ok := v.cache[issuer]; ok && time.Now().Before(c.expiry) {
		return c.keys, nil
	}

	keys, err := fetchJWKS(ctx, issuer)
	if err != nil {
		return nil, err
	}

	v.cache[issuer] = &cachedJWKS{keys: keys, expiry: time.Now().Add(10 * time.Minute)}
	return keys, nil
}

func isLoopback(host string) bool {
	h := strings.TrimSuffix(host, ".")
	if i := strings.LastIndex(h, ":"); i >= 0 {
		h = h[:i]
	}
	return h == "127.0.0.1" || h == "::1" || h == "localhost"
}

// fetchJWKS discovers the JWKS URI from the OIDC discovery document and fetches keys.
func fetchJWKS(ctx context.Context, issuer string) ([]jwkKey, error) {
	parsed, err := url.Parse(issuer)
	if err != nil {
		return nil, fmt.Errorf("invalid issuer URL: %w", err)
	}
	if parsed.Scheme != "https" && !isLoopback(parsed.Host) {
		return nil, fmt.Errorf("issuer must use HTTPS")
	}

	client := &http.Client{Timeout: 10 * time.Second}

	// Discover the JWKS URI via the standard OIDC discovery document.
	discoveryURL := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, "GET", discoveryURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch OIDC discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OIDC discovery returned %d", resp.StatusCode)
	}

	var discovery struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&discovery); err != nil {
		return nil, fmt.Errorf("parse OIDC discovery: %w", err)
	}
	if discovery.JWKSURI == "" {
		return nil, errors.New("OIDC discovery missing jwks_uri")
	}

	if err := validateJWKSURI(issuer, discovery.JWKSURI); err != nil {
		return nil, err
	}

	// Fetch the JWKS.
	req, err = http.NewRequestWithContext(ctx, "GET", discovery.JWKSURI, nil)
	if err != nil {
		return nil, err
	}
	resp, err = client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS endpoint returned %d", resp.StatusCode)
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
		if err := json.Unmarshal(rawKey, &k); err != nil {
			continue
		}
		if k.Kty != "RSA" {
			continue
		}
		pub, err := parseRSAPublicKey(k.N, k.E)
		if err != nil {
			continue
		}
		keys = append(keys, jwkKey{Kid: k.Kid, Pub: pub})
	}
	return keys, nil
}

func validateJWKSURI(issuer, jwksURI string) error {
	issuerURL, err := url.Parse(issuer)
	if err != nil {
		return fmt.Errorf("invalid issuer URL: %w", err)
	}
	jwksURL, err := url.Parse(jwksURI)
	if err != nil {
		return fmt.Errorf("invalid jwks_uri: %w", err)
	}
	if jwksURL.Scheme != "https" && !isLoopback(jwksURL.Host) {
		return fmt.Errorf("jwks_uri must use HTTPS, got %q", jwksURL.Scheme)
	}
	issuerHost := strings.ToLower(issuerURL.Hostname())
	jwksHost := strings.ToLower(jwksURL.Hostname())
	if jwksHost != issuerHost && !strings.HasSuffix(jwksHost, "."+issuerHost) {
		return fmt.Errorf("jwks_uri host %q does not match issuer host %q", jwksHost, issuerHost)
	}
	return nil
}

func parseRSAPublicKey(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := base64URLDecode(nStr)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64URLDecode(eStr)
	if err != nil {
		return nil, err
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)

	if !e.IsInt64() {
		return nil, errors.New("RSA exponent too large")
	}
	eInt := e.Int64()
	// RSA exponents must be odd and >= 3. Standard values are 3, 17, 65537.
	const maxValidExponent = 1<<31 - 1
	if eInt < 3 || eInt > maxValidExponent || eInt%2 == 0 {
		return nil, fmt.Errorf("invalid RSA exponent: %d", eInt)
	}

	return &rsa.PublicKey{N: n, E: int(eInt)}, nil
}

func base64URLDecode(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	return base64.RawURLEncoding.DecodeString(s)
}

func projectFromSubject(subject string) string {
	if !strings.HasPrefix(subject, "repo:") {
		return ""
	}
	rest := subject[len("repo:"):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return ""
	}
	repoPath := rest[:colon]
	slash := strings.LastIndex(repoPath, "/")
	if slash < 0 {
		return ""
	}
	return repoPath[slash+1:]
}

func orgFromSubject(subject string) string {
	if !strings.HasPrefix(subject, "repo:") {
		return ""
	}
	rest := subject[len("repo:"):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return ""
	}
	repoPath := rest[:colon]
	slash := strings.Index(repoPath, "/")
	if slash < 0 {
		return ""
	}
	return repoPath[:slash]
}

func matchSubject(pattern, subject string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, ":*") {
		prefix := pattern[:len(pattern)-1]
		return strings.HasPrefix(subject, prefix)
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := pattern[:len(pattern)-1]
		return strings.HasPrefix(subject, prefix)
	}
	return pattern == subject
}
