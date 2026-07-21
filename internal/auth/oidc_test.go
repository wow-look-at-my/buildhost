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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
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
	v := NewOIDCVerifier(OIDCConfig{})
	token := fakeJWT(
		map[string]any{"alg": "RS256", "kid": "key1"},
		map[string]any{
			"iss": "https://token.actions.githubusercontent.com",
			"sub": "repo:org/repo:ref:refs/heads/main",
			"exp": time.Now().Add(-1 * time.Hour).Unix(),
		},
	)
	policies := []db.OIDCPolicy{{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "*",
		Scopes:         "read,write",
	}}
	_, _, err := v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token expired")
}

func TestVerifyToken_RejectsNotYetValidToken(t *testing.T) {
	v := NewOIDCVerifier(OIDCConfig{})
	token := fakeJWT(
		map[string]any{"alg": "RS256", "kid": "key1"},
		map[string]any{
			"iss": "https://token.actions.githubusercontent.com",
			"sub": "repo:org/repo:ref:refs/heads/main",
			"exp": time.Now().Add(1 * time.Hour).Unix(),
			"nbf": time.Now().Add(1 * time.Hour).Unix(),
		},
	)
	policies := []db.OIDCPolicy{{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "*",
		Scopes:         "read,write",
	}}
	_, _, err := v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token not yet valid")
}

func TestVerifyToken_RejectsUnsupportedAlgorithm(t *testing.T) {
	v := NewOIDCVerifier(OIDCConfig{})
	token := fakeJWT(
		map[string]any{"alg": "HS256", "kid": "key1"},
		map[string]any{
			"iss": "https://token.actions.githubusercontent.com",
			"sub": "repo:org/repo:ref:refs/heads/main",
			"exp": time.Now().Add(1 * time.Hour).Unix(),
		},
	)
	policies := []db.OIDCPolicy{{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "*",
		Scopes:         "read,write",
	}}
	// HS256 doesn't produce valid JWTs that ParseUnverified can handle the
	// same way, but the keyfunc will reject the algorithm during verified parse.
	_, _, err := v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
}

func TestVerifyToken_RejectsNonJWT(t *testing.T) {
	v := NewOIDCVerifier(OIDCConfig{})
	policies := []db.OIDCPolicy{{
		Issuer:         "https://example.com",
		SubjectPattern: "*",
		Scopes:         "read",
	}}
	_, _, err := v.VerifyToken(context.Background(), "not-a-jwt", policies)
	require.Error(t, err)
}

func TestVerifyToken_RejectsNoMatchingPolicy(t *testing.T) {
	v := NewOIDCVerifier(OIDCConfig{})
	token := fakeJWT(
		map[string]any{"alg": "RS256", "kid": "key1"},
		map[string]any{
			"iss": "https://token.actions.githubusercontent.com",
			"sub": "repo:org/repo:ref:refs/heads/main",
			"exp": time.Now().Add(1 * time.Hour).Unix(),
		},
	)
	policies := []db.OIDCPolicy{{
		Issuer:         "https://other-issuer.example.com",
		SubjectPattern: "*",
		Scopes:         "read,write",
	}}
	_, _, err := v.VerifyToken(context.Background(), token, policies)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOIDCNotMatched)
}

func TestVerifyToken_RejectsNonMatchingSubject(t *testing.T) {
	v := NewOIDCVerifier(OIDCConfig{})
	token := fakeJWT(
		map[string]any{"alg": "RS256", "kid": "key1"},
		map[string]any{
			"iss": "https://token.actions.githubusercontent.com",
			"sub": "repo:org/other-repo:ref:refs/heads/main",
			"exp": time.Now().Add(1 * time.Hour).Unix(),
		},
	)
	policies := []db.OIDCPolicy{{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "repo:org/specific-repo:*",
		Scopes:         "read,write",
	}}
	_, _, err := v.VerifyToken(context.Background(), token, policies)
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

func TestProjectFromSubject_UppercaseNormalized(t *testing.T) {
	assert.Equal(t, "myrepo", projectFromSubject("repo:MyOrg/MyRepo:ref:refs/heads/main"))
}

func TestProjectFromSubject_InvalidCharsRejected(t *testing.T) {
	assert.Equal(t, "", projectFromSubject("repo:org/my repo:ref:refs/heads/main"))
	assert.Equal(t, "", projectFromSubject("repo:org/@bad:ref:refs/heads/main"))
}
