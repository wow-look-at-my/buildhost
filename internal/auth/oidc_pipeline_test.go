package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// --- Full pipeline tests with real RSA keys ---

func signJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid})
	payload, _ := json.Marshal(claims)
	content := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	hash := sha256.Sum256([]byte(content))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	require.NoError(t, err)
	return content + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func jwksServer(t *testing.T, pub *rsa.PublicKey, kid string) *httptest.Server {
	t.Helper()
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1})
	// Marshalled, not formatted: a quote or a backslash in a value would break a formatted document.
	jwksBody, err := json.Marshal(map[string]any{
		"keys": []map[string]string{{"kty": "RSA", "kid": kid, "n": n, "e": e}},
	})
	require.NoError(t, err)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/.well-known/openid-configuration" {
			discovery, err := json.Marshal(map[string]string{"jwks_uri": srv.URL + "/.well-known/jwks"})
			require.NoError(t, err)
			w.Write(discovery)
			return
		}
		w.Write(jwksBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestVerifyToken_FullPipeline_ValidJWT(t *testing.T) {
	t.Serial()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-1")

	claims := map[string]any{
		"iss": srv.URL,
		"sub": "repo:myorg/myrepo:ref:refs/heads/main",
		"exp": time.Now().Add(10 * time.Minute).Unix(),
		"iat": time.Now().Unix(),
	}
	token := signJWT(t, key, "kid-1", claims)

	projID := int64(42)
	policies := []db.OIDCPolicy{{
		Issuer:         srv.URL,
		SubjectPattern: "repo:myorg/myrepo:*",
		ProjectID:      &projID,
		Scopes:         "read,write",
	}}

	v := NewOIDCVerifier(OIDCConfig{})
	tok, _, err := v.VerifyToken(context.Background(), token, policies)
	require.NoError(t, err)
	assert.Equal(t, "read,write", tok.Scopes)
	assert.Equal(t, int64(42), *tok.ProjectID)
	assert.Contains(t, tok.Name, "repo:myorg/myrepo")
}

func TestVerifyToken_FullPipeline_ExpiredJWT(t *testing.T) {
	t.Serial()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-2")

	claims := map[string]any{
		"iss": srv.URL,
		"sub": "repo:myorg/myrepo:ref:refs/heads/main",
		"exp": time.Now().Add(-10 * time.Minute).Unix(),
	}
	token := signJWT(t, key, "kid-2", claims)

	policies := []db.OIDCPolicy{{
		Issuer:         srv.URL,
		SubjectPattern: "repo:myorg/myrepo:*",
		Scopes:         "read",
	}}

	v := NewOIDCVerifier(OIDCConfig{})
	_, _, err = v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestVerifyToken_FullPipeline_WrongSignature(t *testing.T) {
	t.Serial()
	key1, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	key2, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key1.PublicKey, "kid-3")

	claims := map[string]any{
		"iss": srv.URL,
		"sub": "repo:myorg/myrepo:ref:refs/heads/main",
		"exp": time.Now().Add(10 * time.Minute).Unix(),
	}
	token := signJWT(t, key2, "kid-3", claims)

	policies := []db.OIDCPolicy{{
		Issuer:         srv.URL,
		SubjectPattern: "repo:myorg/myrepo:*",
		Scopes:         "read",
	}}

	v := NewOIDCVerifier(OIDCConfig{})
	_, _, err = v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature")
}

func TestVerifyToken_FullPipeline_GlobalPolicy(t *testing.T) {
	t.Serial()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-4")

	claims := map[string]any{
		"iss": srv.URL,
		"sub": "repo:myorg/myrepo:ref:refs/heads/main",
		"exp": time.Now().Add(10 * time.Minute).Unix(),
	}
	token := signJWT(t, key, "kid-4", claims)

	policies := []db.OIDCPolicy{{
		Issuer:         srv.URL,
		SubjectPattern: "*",
		Scopes:         "read",
	}}

	v := NewOIDCVerifier(OIDCConfig{})
	tok, _, err := v.VerifyToken(context.Background(), token, policies)
	require.NoError(t, err)
	assert.Nil(t, tok.ProjectID)
	assert.Equal(t, "read", tok.Scopes)
}

func TestParseRSAPublicKey(t *testing.T) {
	t.Serial()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	n := base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1})

	pub, err := parseRSAPublicKey(n, e)
	require.NoError(t, err)
	assert.Equal(t, key.PublicKey.N, pub.N)
	assert.Equal(t, 65537, pub.E)
}

func TestParseRSAPublicKey_InvalidExponent(t *testing.T) {
	t.Serial()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	n := base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes())

	e1 := base64.RawURLEncoding.EncodeToString([]byte{1})
	_, err = parseRSAPublicKey(n, e1)
	assert.Error(t, err)

	e2 := base64.RawURLEncoding.EncodeToString([]byte{2})
	_, err = parseRSAPublicKey(n, e2)
	assert.Error(t, err)
}

func TestVerifyToken_RejectsTokenWithNoExpiry(t *testing.T) {
	t.Serial()
	v := NewOIDCVerifier(OIDCConfig{})
	token := fakeJWT(
		map[string]any{"alg": "RS256", "kid": "key1"},
		map[string]any{
			"iss": "https://token.actions.githubusercontent.com",
			"sub": "repo:org/repo:ref:refs/heads/main",
		},
	)
	policies := []db.OIDCPolicy{{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "*",
		Scopes:         "read,write",
	}}
	_, _, err := v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing exp claim")
}

func TestVerifyToken_FullPipeline_AudienceMatch(t *testing.T) {
	t.Serial()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-aud-ok")

	claims := map[string]any{
		"iss": srv.URL,
		"sub": "repo:myorg/myrepo:ref:refs/heads/main",
		"aud": "https://buildhost.example.com",
		"exp": time.Now().Add(10 * time.Minute).Unix(),
	}
	token := signJWT(t, key, "kid-aud-ok", claims)

	policies := []db.OIDCPolicy{{
		Issuer:         srv.URL,
		SubjectPattern: "repo:myorg/myrepo:*",
		Audience:       "https://buildhost.example.com",
		Scopes:         "read",
	}}

	v := NewOIDCVerifier(OIDCConfig{})
	tok, _, err := v.VerifyToken(context.Background(), token, policies)
	require.NoError(t, err)
	assert.Equal(t, "read", tok.Scopes)
}

func TestVerifyToken_FullPipeline_AudienceMismatch(t *testing.T) {
	t.Serial()
	v := NewOIDCVerifier(OIDCConfig{})
	token := fakeJWT(
		map[string]any{"alg": "RS256", "kid": "key1"},
		map[string]any{
			"iss": "https://token.actions.githubusercontent.com",
			"sub": "repo:org/repo:ref:refs/heads/main",
			"aud": "https://other-service.example.com",
			"exp": time.Now().Add(1 * time.Hour).Unix(),
		},
	)
	policies := []db.OIDCPolicy{{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "*",
		Audience:       "https://buildhost.example.com",
		Scopes:         "read",
	}}
	_, _, err := v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audience")
}

func TestVerifyToken_FullPipeline_NoAudienceInPolicy_AnyAudienceAccepted(t *testing.T) {
	t.Serial()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-noaud")

	claims := map[string]any{
		"iss": srv.URL,
		"sub": "repo:myorg/myrepo:ref:refs/heads/main",
		"aud": "https://some-other-service.example.com",
		"exp": time.Now().Add(10 * time.Minute).Unix(),
	}
	token := signJWT(t, key, "kid-noaud", claims)

	policies := []db.OIDCPolicy{{
		Issuer:         srv.URL,
		SubjectPattern: "repo:myorg/myrepo:*",
		Scopes:         "read",
	}}

	v := NewOIDCVerifier(OIDCConfig{})
	tok, _, err := v.VerifyToken(context.Background(), token, policies)
	require.NoError(t, err)
	assert.Equal(t, "read", tok.Scopes)
}
