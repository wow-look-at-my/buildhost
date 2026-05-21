package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

// --- LooksLikeJWT tests ---

func TestLooksLikeJWT_ValidThreeParts(t *testing.T) {
	token := strings.Repeat("a", 40) + "." + strings.Repeat("b", 40) + "." + strings.Repeat("c", 40)
	assert.True(t, LooksLikeJWT(token))
}

func TestLooksLikeJWT_TooShort(t *testing.T) {
	token := "aaa.bbb.ccc"
	assert.False(t, LooksLikeJWT(token))
}

func TestLooksLikeJWT_TwoParts(t *testing.T) {
	token := strings.Repeat("a", 60) + "." + strings.Repeat("b", 60)
	assert.False(t, LooksLikeJWT(token))
}

func TestLooksLikeJWT_FourParts(t *testing.T) {
	token := strings.Repeat("a", 30) + "." + strings.Repeat("b", 30) + "." + strings.Repeat("c", 30) + "." + strings.Repeat("d", 30)
	assert.False(t, LooksLikeJWT(token))
}

func TestLooksLikeJWT_OnePart(t *testing.T) {
	token := strings.Repeat("x", 200)
	assert.False(t, LooksLikeJWT(token))
}

func TestLooksLikeJWT_EmptyString(t *testing.T) {
	assert.False(t, LooksLikeJWT(""))
}

func TestLooksLikeJWT_PlainBearerToken(t *testing.T) {
	token := "bh_a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0"
	assert.False(t, LooksLikeJWT(token))
}

// --- matchSubject tests ---

func TestMatchSubject_ExactMatch(t *testing.T) {
	assert.True(t, matchSubject("repo:org/name:ref:refs/heads/main", "repo:org/name:ref:refs/heads/main"))
}

func TestMatchSubject_ExactMismatch(t *testing.T) {
	assert.False(t, matchSubject("repo:org/name:ref:refs/heads/main", "repo:org/other:ref:refs/heads/main"))
}

func TestMatchSubject_Wildcard(t *testing.T) {
	assert.True(t, matchSubject("*", "anything-at-all"))
	assert.True(t, matchSubject("*", ""))
}

func TestMatchSubject_PrefixStar(t *testing.T) {
	assert.True(t, matchSubject("repo:org/name*", "repo:org/name:ref:refs/heads/main"))
	assert.True(t, matchSubject("repo:org/name*", "repo:org/name"))
	assert.False(t, matchSubject("repo:org/name*", "repo:org/other"))
}

func TestMatchSubject_ColonStar(t *testing.T) {
	assert.True(t, matchSubject("repo:org/name:*", "repo:org/name:ref:refs/heads/main"))
	assert.True(t, matchSubject("repo:org/name:*", "repo:org/name:anything"))
	assert.False(t, matchSubject("repo:org/name:*", "repo:org/nameSOMETHING"))
}

func TestMatchSubject_EmptyPattern(t *testing.T) {
	assert.True(t, matchSubject("", ""))
	assert.False(t, matchSubject("", "nonempty"))
}

func TestMatchSubject_PrefixStarNoMatch(t *testing.T) {
	assert.False(t, matchSubject("prefix*", "other"))
}

// --- base64URLDecode tests ---

func TestBase64URLDecode_Standard(t *testing.T) {
	input := base64.RawURLEncoding.EncodeToString([]byte("hello world"))
	decoded, err := base64URLDecode(input)
	require.NoError(t, err)
	assert.Equal(t, []byte("hello world"), decoded)
}

func TestBase64URLDecode_WithPadding(t *testing.T) {
	input := base64.URLEncoding.EncodeToString([]byte("test"))
	decoded, err := base64URLDecode(input)
	require.NoError(t, err)
	assert.Equal(t, []byte("test"), decoded)
}

func TestBase64URLDecode_URLSafeCharacters(t *testing.T) {
	data := []byte{0xfb, 0xff, 0xfe}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	decoded, err := base64URLDecode(encoded)
	require.NoError(t, err)
	assert.Equal(t, data, decoded)
}

func TestBase64URLDecode_EmptyString(t *testing.T) {
	decoded, err := base64URLDecode("")
	require.NoError(t, err)
	assert.Equal(t, []byte{}, decoded)
}

func TestBase64URLDecode_InvalidCharacters(t *testing.T) {
	_, err := base64URLDecode("!!!invalid!!!")
	assert.Error(t, err)
}

// --- helpers for constructing fake JWTs ---

func fakeJWT(header, claims map[string]any) string {
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(h) + "." +
		base64.RawURLEncoding.EncodeToString(c) + "." +
		base64.RawURLEncoding.EncodeToString([]byte("fake-signature"))
}

// --- VerifyToken tests (expired / malformed) ---

func TestVerifyToken_RejectsExpiredToken(t *testing.T) {
	v := NewOIDCVerifier(nil, nil)
	token := fakeJWT(
		map[string]any{"alg": "RS256", "kid": "key1"},
		map[string]any{
			"iss": "https://token.actions.githubusercontent.com",
			"sub": "repo:org/repo:ref:refs/heads/main",
			"exp": time.Now().Add(-1 * time.Hour).Unix(),
		},
	)
	policies := []model.OIDCPolicy{{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "*",
		Scopes:         "read,write",
	}}
	_, err := v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token expired")
}

func TestVerifyToken_RejectsNotYetValidToken(t *testing.T) {
	v := NewOIDCVerifier(nil, nil)
	token := fakeJWT(
		map[string]any{"alg": "RS256", "kid": "key1"},
		map[string]any{
			"iss": "https://token.actions.githubusercontent.com",
			"sub": "repo:org/repo:ref:refs/heads/main",
			"exp": time.Now().Add(1 * time.Hour).Unix(),
			"nbf": time.Now().Add(1 * time.Hour).Unix(),
		},
	)
	policies := []model.OIDCPolicy{{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "*",
		Scopes:         "read,write",
	}}
	_, err := v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token not yet valid")
}

func TestVerifyToken_RejectsUnsupportedAlgorithm(t *testing.T) {
	v := NewOIDCVerifier(nil, nil)
	token := fakeJWT(
		map[string]any{"alg": "HS256", "kid": "key1"},
		map[string]any{
			"iss": "https://token.actions.githubusercontent.com",
			"sub": "repo:org/repo:ref:refs/heads/main",
			"exp": time.Now().Add(1 * time.Hour).Unix(),
		},
	)
	policies := []model.OIDCPolicy{{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "*",
		Scopes:         "read,write",
	}}
	// HS256 doesn't produce valid JWTs that ParseUnverified can handle the
	// same way, but the keyfunc will reject the algorithm during verified parse.
	_, err := v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
}

func TestVerifyToken_RejectsNonJWT(t *testing.T) {
	v := NewOIDCVerifier(nil, nil)
	policies := []model.OIDCPolicy{{
		Issuer:         "https://example.com",
		SubjectPattern: "*",
		Scopes:         "read",
	}}
	_, err := v.VerifyToken(context.Background(), "not-a-jwt", policies)
	require.Error(t, err)
}

func TestVerifyToken_RejectsNoMatchingPolicy(t *testing.T) {
	v := NewOIDCVerifier(nil, nil)
	token := fakeJWT(
		map[string]any{"alg": "RS256", "kid": "key1"},
		map[string]any{
			"iss": "https://token.actions.githubusercontent.com",
			"sub": "repo:org/repo:ref:refs/heads/main",
			"exp": time.Now().Add(1 * time.Hour).Unix(),
		},
	)
	policies := []model.OIDCPolicy{{
		Issuer:         "https://other-issuer.example.com",
		SubjectPattern: "*",
		Scopes:         "read,write",
	}}
	_, err := v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOIDCNotMatched)
}

func TestVerifyToken_RejectsNonMatchingSubject(t *testing.T) {
	v := NewOIDCVerifier(nil, nil)
	token := fakeJWT(
		map[string]any{"alg": "RS256", "kid": "key1"},
		map[string]any{
			"iss": "https://token.actions.githubusercontent.com",
			"sub": "repo:org/other-repo:ref:refs/heads/main",
			"exp": time.Now().Add(1 * time.Hour).Unix(),
		},
	)
	policies := []model.OIDCPolicy{{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "repo:org/specific-repo:*",
		Scopes:         "read,write",
	}}
	_, err := v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOIDCNotMatched)
}

// --- projectFromSubject tests ---

func TestProjectFromSubject_GHA(t *testing.T) {
	assert.Equal(t, "myrepo", projectFromSubject("repo:myorg/myrepo:ref:refs/heads/main"))
}

func TestProjectFromSubject_NestedOrg(t *testing.T) {
	assert.Equal(t, "myrepo", projectFromSubject("repo:myorg/sub/myrepo:ref:refs/heads/main"))
}

func TestProjectFromSubject_NoPrefix(t *testing.T) {
	assert.Equal(t, "", projectFromSubject("something:else"))
}

func TestProjectFromSubject_NoColon(t *testing.T) {
	assert.Equal(t, "", projectFromSubject("repo:myorg/myrepo"))
}

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
	jwksBody := fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":"%s","n":"%s","e":"%s"}]}`, kid, n, e)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/.well-known/openid-configuration" {
			fmt.Fprintf(w, `{"jwks_uri":"%s/.well-known/jwks"}`, srv.URL)
			return
		}
		w.Write([]byte(jwksBody))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestVerifyToken_FullPipeline_ValidJWT(t *testing.T) {
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
	policies := []model.OIDCPolicy{{
		Issuer:         srv.URL,
		SubjectPattern: "repo:myorg/myrepo:*",
		ProjectID:      &projID,
		Scopes:         "read,write",
	}}

	v := NewOIDCVerifier(nil, nil)
	tok, err := v.VerifyToken(context.Background(), token, policies)
	require.NoError(t, err)
	assert.Equal(t, "read,write", tok.Scopes)
	assert.Equal(t, int64(42), *tok.ProjectID)
	assert.Contains(t, tok.Name, "repo:myorg/myrepo")
}

func TestVerifyToken_FullPipeline_ExpiredJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-2")

	claims := map[string]any{
		"iss": srv.URL,
		"sub": "repo:myorg/myrepo:ref:refs/heads/main",
		"exp": time.Now().Add(-10 * time.Minute).Unix(),
	}
	token := signJWT(t, key, "kid-2", claims)

	policies := []model.OIDCPolicy{{
		Issuer:         srv.URL,
		SubjectPattern: "repo:myorg/myrepo:*",
		Scopes:         "read",
	}}

	v := NewOIDCVerifier(nil, nil)
	_, err = v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestVerifyToken_FullPipeline_WrongSignature(t *testing.T) {
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

	policies := []model.OIDCPolicy{{
		Issuer:         srv.URL,
		SubjectPattern: "repo:myorg/myrepo:*",
		Scopes:         "read",
	}}

	v := NewOIDCVerifier(nil, nil)
	_, err = v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature")
}

func TestVerifyToken_FullPipeline_GlobalPolicy(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-4")

	claims := map[string]any{
		"iss": srv.URL,
		"sub": "repo:myorg/myrepo:ref:refs/heads/main",
		"exp": time.Now().Add(10 * time.Minute).Unix(),
	}
	token := signJWT(t, key, "kid-4", claims)

	policies := []model.OIDCPolicy{{
		Issuer:         srv.URL,
		SubjectPattern: "*",
		Scopes:         "read",
	}}

	v := NewOIDCVerifier(nil, nil)
	tok, err := v.VerifyToken(context.Background(), token, policies)
	require.NoError(t, err)
	assert.Nil(t, tok.ProjectID)
	assert.Equal(t, "read", tok.Scopes)
}

func TestParseRSAPublicKey(t *testing.T) {
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
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	n := base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes())

	// Exponent of 1 is invalid (< 3).
	e1 := base64.RawURLEncoding.EncodeToString([]byte{1})
	_, err = parseRSAPublicKey(n, e1)
	assert.Error(t, err)

	// Exponent of 2 is invalid (even).
	e2 := base64.RawURLEncoding.EncodeToString([]byte{2})
	_, err = parseRSAPublicKey(n, e2)
	assert.Error(t, err)
}

func TestVerifyToken_RejectsTokenWithNoExpiry(t *testing.T) {
	v := NewOIDCVerifier(nil, nil)
	token := fakeJWT(
		map[string]any{"alg": "RS256", "kid": "key1"},
		map[string]any{
			"iss": "https://token.actions.githubusercontent.com",
			"sub": "repo:org/repo:ref:refs/heads/main",
		},
	)
	policies := []model.OIDCPolicy{{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "*",
		Scopes:         "read,write",
	}}
	_, err := v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing exp claim")
}

func TestVerifyToken_FullPipeline_AudienceMatch(t *testing.T) {
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

	policies := []model.OIDCPolicy{{
		Issuer:         srv.URL,
		SubjectPattern: "repo:myorg/myrepo:*",
		Audience:       "https://buildhost.example.com",
		Scopes:         "read",
	}}

	v := NewOIDCVerifier(nil, nil)
	tok, err := v.VerifyToken(context.Background(), token, policies)
	require.NoError(t, err)
	assert.Equal(t, "read", tok.Scopes)
}

func TestVerifyToken_FullPipeline_AudienceMismatch(t *testing.T) {
	v := NewOIDCVerifier(nil, nil)
	token := fakeJWT(
		map[string]any{"alg": "RS256", "kid": "key1"},
		map[string]any{
			"iss": "https://token.actions.githubusercontent.com",
			"sub": "repo:org/repo:ref:refs/heads/main",
			"aud": "https://other-service.example.com",
			"exp": time.Now().Add(1 * time.Hour).Unix(),
		},
	)
	policies := []model.OIDCPolicy{{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "*",
		Audience:       "https://buildhost.example.com",
		Scopes:         "read",
	}}
	_, err := v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audience")
}

func TestVerifyToken_FullPipeline_NoAudienceInPolicy_AnyAudienceAccepted(t *testing.T) {
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

	policies := []model.OIDCPolicy{{
		Issuer:         srv.URL,
		SubjectPattern: "repo:myorg/myrepo:*",
		Scopes:         "read",
	}}

	v := NewOIDCVerifier(nil, nil)
	tok, err := v.VerifyToken(context.Background(), token, policies)
	require.NoError(t, err)
	assert.Equal(t, "read", tok.Scopes)
}

func TestVerifyToken_TrustedIssuer_NoPolicies(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-trusted")

	claims := map[string]any{
		"iss": srv.URL,
		"sub": "repo:myorg/myrepo:ref:refs/heads/main",
		"exp": time.Now().Add(10 * time.Minute).Unix(),
		"iat": time.Now().Unix(),
	}
	token := signJWT(t, key, "kid-trusted", claims)

	v := NewOIDCVerifier([]string{srv.URL}, []string{"*"})
	tok, err := v.VerifyToken(context.Background(), token, nil)
	require.NoError(t, err)
	assert.Equal(t, "read,write", tok.Scopes)
	assert.Equal(t, "myrepo", tok.OIDCProject)
	assert.Equal(t, "oidc:repo:myorg/myrepo:ref:refs/heads/main", tok.Name)
}

func TestVerifyToken_UntrustedIssuer_NoPolicies(t *testing.T) {
	v := NewOIDCVerifier([]string{"https://trusted.example.com"}, nil)
	token := fakeJWT(
		map[string]any{"alg": "RS256", "kid": "key1"},
		map[string]any{
			"iss": "https://untrusted.example.com",
			"sub": "repo:myorg/myrepo:ref:refs/heads/main",
			"exp": time.Now().Add(1 * time.Hour).Unix(),
		},
	)
	_, err := v.VerifyToken(context.Background(), token, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOIDCNotMatched)
}
