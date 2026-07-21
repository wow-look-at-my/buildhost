package auth

// JWKS discovery, fetching, caching, and RSA key parsing for OIDC token
// verification. Split from oidc.go, which holds the verification flow and
// subject/claim parsing.

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
	"strings"
	"time"
)

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

	pub := &rsa.PublicKey{N: n, E: int(eInt)}
	if pub.N.BitLen() < 2048 {
		return nil, fmt.Errorf("RSA key too small: %d bits (minimum 2048)", pub.N.BitLen())
	}
	return pub, nil
}

func base64URLDecode(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	return base64.RawURLEncoding.DecodeString(s)
}
