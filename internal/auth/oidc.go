package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

var ErrOIDCNotMatched = errors.New("no matching OIDC policy")

type OIDCVerifier struct {
	mu    sync.RWMutex
	cache map[string]*cachedJWKS
}

type cachedJWKS struct {
	keys   []jwkKey
	expiry time.Time
}

type jwkKey struct {
	Kid string
	Pub *rsa.PublicKey
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type jwtClaims struct {
	Issuer     string `json:"iss"`
	Subject    string `json:"sub"`
	Audience   any    `json:"aud"`
	Expiry     int64  `json:"exp"`
	NotBefore  int64  `json:"nbf"`
	IssuedAt   int64  `json:"iat"`
	Repository string `json:"repository"`
}

func NewOIDCVerifier() *OIDCVerifier {
	return &OIDCVerifier{cache: make(map[string]*cachedJWKS)}
}

func LooksLikeJWT(token string) bool {
	parts := strings.Split(token, ".")
	return len(parts) == 3 && len(token) > 100
}

func (v *OIDCVerifier) VerifyToken(ctx context.Context, raw string, policies []model.OIDCPolicy) (*model.APIToken, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("not a JWT")
	}

	headerBytes, err := base64URLDecode(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}

	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}
	if header.Alg != "RS256" {
		return nil, fmt.Errorf("unsupported algorithm: %s", header.Alg)
	}

	payloadBytes, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	var claims jwtClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}

	// exp is required — tokens with no expiry are not accepted.
	if claims.Expiry == 0 {
		return nil, errors.New("token missing exp claim")
	}
	const clockSkew = 60
	now := time.Now().Unix()
	if now > claims.Expiry+clockSkew {
		return nil, errors.New("token expired")
	}
	if claims.NotBefore != 0 && now < claims.NotBefore-60 {
		return nil, errors.New("token not yet valid")
	}

	var matchedPolicy *model.OIDCPolicy
	for i := range policies {
		p := &policies[i]
		if p.Issuer != claims.Issuer {
			continue
		}
		if matchSubject(p.SubjectPattern, claims.Subject) {
			matchedPolicy = p
			break
		}
	}
	if matchedPolicy == nil {
		return nil, ErrOIDCNotMatched
	}

	// Validate audience if the policy specifies one.
	if matchedPolicy.Audience != "" {
		if !audienceContains(claims.Audience, matchedPolicy.Audience) {
			return nil, errors.New("token audience does not match policy")
		}
	}

	keys, err := v.getKeys(ctx, claims.Issuer)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}

	sig, err := base64URLDecode(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	signedContent := []byte(parts[0] + "." + parts[1])
	hash := sha256.Sum256(signedContent)

	var verified bool
	for _, key := range keys {
		if header.Kid != "" && key.Kid != header.Kid {
			continue
		}
		if err := rsa.VerifyPKCS1v15(key.Pub, crypto.SHA256, hash[:], sig); err == nil {
			verified = true
			break
		}
	}
	if !verified {
		return nil, errors.New("signature verification failed")
	}

	return &model.APIToken{
		ID:        -1,
		Name:      "oidc:" + claims.Subject,
		ProjectID: matchedPolicy.ProjectID,
		Scopes:    matchedPolicy.Scopes,
	}, nil
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

// audienceContains checks if expected appears in the JWT aud claim (string or []string).
func audienceContains(aud any, expected string) bool {
	switch v := aud.(type) {
	case string:
		return v == expected
	case []any:
		for _, a := range v {
			if s, ok := a.(string); ok && s == expected {
				return true
			}
		}
	}
	return false
}
