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
	"math/big"
	"net/http"
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

	now := time.Now().Unix()
	if claims.Expiry != 0 && now > claims.Expiry {
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

func fetchJWKS(ctx context.Context, issuer string) ([]jwkKey, error) {
	url := strings.TrimSuffix(issuer, "/") + "/.well-known/jwks"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS endpoint returned %d", resp.StatusCode)
	}

	var raw struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
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
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
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
