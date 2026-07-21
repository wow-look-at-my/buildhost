package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyToken_TrustedIssuer_NoPolicies(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-trusted")

	claims := map[string]any{
		"iss":        srv.URL,
		"sub":        "repo:myorg/myrepo:ref:refs/heads/main",
		"aud":        "https://buildhost.example.com",
		"exp":        time.Now().Add(10 * time.Minute).Unix(),
		"iat":        time.Now().Unix(),
		"event_name": "push",
	}
	token := signJWT(t, key, "kid-trusted", claims)

	v := NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{srv.URL}, AllowedOrgs: []string{"*"}, AllowedEvents: []string{"push"}})
	var vr VerifyResult
	tok, oidcProject, err := v.VerifyTokenFull(context.Background(), token, nil, &vr)
	require.NoError(t, err)
	assert.Equal(t, "read,write", tok.Scopes)
	assert.Equal(t, "myrepo", oidcProject)
	assert.Equal(t, "oidc:repo:myorg/myrepo:ref:refs/heads/main", tok.Name)
	assert.True(t, vr.OIDCPrivate, "missing repository_visibility should default to private")
}

func TestVerifyToken_TrustedIssuer_AllowedEvent(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-event-ok")

	claims := map[string]any{
		"iss":        srv.URL,
		"sub":        "repo:myorg/myrepo:ref:refs/heads/main",
		"aud":        "https://buildhost.example.com",
		"exp":        time.Now().Add(10 * time.Minute).Unix(),
		"iat":        time.Now().Unix(),
		"event_name": "push",
	}
	token := signJWT(t, key, "kid-event-ok", claims)

	v := NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{srv.URL}, AllowedOrgs: []string{"*"}, AllowedEvents: []string{"push"}})
	_, oidcProject, err := v.VerifyToken(context.Background(), token, nil)
	require.NoError(t, err)
	assert.Equal(t, "myrepo", oidcProject)
}

// TestVerifyToken_TrustedIssuer_OrgCaseInsensitive proves the org allowlist
// matches case-insensitively: a lowercase allowlist entry ("pazerop") still
// authorizes a subject carrying GitHub's canonical mixed-case org ("PazerOP").
// This is the exact PazerOP/scratch PR-preview scenario -- a pure casing
// mismatch must not silently block auto-provisioning.
func TestVerifyToken_TrustedIssuer_OrgCaseInsensitive(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-org-case")

	claims := map[string]any{
		"iss":        srv.URL,
		"sub":        "repo:PazerOP/scratch:pull_request",
		"exp":        time.Now().Add(10 * time.Minute).Unix(),
		"iat":        time.Now().Unix(),
		"event_name": "pull_request",
	}
	token := signJWT(t, key, "kid-org-case", claims)

	v := NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{srv.URL}, AllowedOrgs: []string{"pazerop"}, AllowedEvents: []string{"pull_request"}})
	_, oidcProject, err := v.VerifyToken(context.Background(), token, nil)
	require.NoError(t, err)
	assert.Equal(t, "scratch", oidcProject)
}

func TestVerifyToken_TrustedIssuer_AutoProvision(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-aud-auto")

	claims := map[string]any{
		"iss":        srv.URL,
		"sub":        "repo:myorg/myrepo:ref:refs/heads/main",
		"aud":        "https://buildhost.example.com",
		"exp":        time.Now().Add(10 * time.Minute).Unix(),
		"event_name": "push",
	}
	token := signJWT(t, key, "kid-aud-auto", claims)

	v := NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{srv.URL}, AllowedOrgs: []string{"*"}, AllowedEvents: []string{"push"}})
	_, oidcProject, err := v.VerifyToken(context.Background(), token, nil)
	require.NoError(t, err)
	assert.Equal(t, "myrepo", oidcProject)
}

func TestVerifyToken_TrustedIssuer_AudienceIgnored(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-aud-other")

	// A token minted for a different service still auto-provisions: the audience
	// is no longer gated (trust = issuer signature + org + event + subject).
	claims := map[string]any{
		"iss":        srv.URL,
		"sub":        "repo:myorg/myrepo:ref:refs/heads/main",
		"aud":        "https://other-service.example.com",
		"exp":        time.Now().Add(10 * time.Minute).Unix(),
		"event_name": "push",
	}
	token := signJWT(t, key, "kid-aud-other", claims)

	v := NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{srv.URL}, AllowedOrgs: []string{"*"}, AllowedEvents: []string{"push"}})
	_, oidcProject, err := v.VerifyToken(context.Background(), token, nil)
	require.NoError(t, err)
	assert.Equal(t, "myrepo", oidcProject)
}

func TestVerifyToken_TrustedIssuer_PrivateRepoVisibility(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-vis-priv")

	claims := map[string]any{
		"iss":                   srv.URL,
		"sub":                   "repo:myorg/myrepo:ref:refs/heads/main",
		"aud":                   "https://buildhost.example.com",
		"exp":                   time.Now().Add(10 * time.Minute).Unix(),
		"iat":                   time.Now().Unix(),
		"event_name":            "push",
		"repository_visibility": "private",
	}
	token := signJWT(t, key, "kid-vis-priv", claims)

	v := NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{srv.URL}, AllowedOrgs: []string{"*"}, AllowedEvents: []string{"push"}})
	var vr VerifyResult
	_, _, err = v.VerifyTokenFull(context.Background(), token, nil, &vr)
	require.NoError(t, err)
	assert.True(t, vr.OIDCPrivate)
}

func TestVerifyToken_TrustedIssuer_PublicRepoVisibility(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-vis-pub")

	claims := map[string]any{
		"iss":                   srv.URL,
		"sub":                   "repo:myorg/myrepo:ref:refs/heads/main",
		"aud":                   "https://buildhost.example.com",
		"exp":                   time.Now().Add(10 * time.Minute).Unix(),
		"iat":                   time.Now().Unix(),
		"event_name":            "push",
		"repository_visibility": "public",
	}
	token := signJWT(t, key, "kid-vis-pub", claims)

	v := NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{srv.URL}, AllowedOrgs: []string{"*"}, AllowedEvents: []string{"push"}})
	var vr VerifyResult
	_, _, err = v.VerifyTokenFull(context.Background(), token, nil, &vr)
	require.NoError(t, err)
	assert.False(t, vr.OIDCPrivate)
}

func TestVerifyToken_UntrustedIssuer_NoPolicies(t *testing.T) {
	v := NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{"https://trusted.example.com"}})
	token := fakeJWT(
		map[string]any{"alg": "RS256", "kid": "key1"},
		map[string]any{
			"iss": "https://untrusted.example.com",
			"sub": "repo:myorg/myrepo:ref:refs/heads/main",
			"exp": time.Now().Add(1 * time.Hour).Unix(),
		},
	)
	_, _, err := v.VerifyToken(context.Background(), token, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOIDCNotMatched)
}
