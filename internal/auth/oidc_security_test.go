package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"math/big"
	"testing"
	"time"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestVerifyToken_TrustedIssuer_EmptyEventRejected(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-no-event")

	claims := map[string]any{
		"iss": srv.URL,
		"sub": "repo:myorg/myrepo:ref:refs/heads/main",
		"aud": "https://buildhost.example.com",
		"exp": time.Now().Add(10 * time.Minute).Unix(),
	}
	token := signJWT(t, key, "kid-no-event", claims)

	v := NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{srv.URL}, AllowedOrgs: []string{"*"}, AllowedEvents: []string{"push"}})
	_, _, err = v.VerifyToken(context.Background(), token, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in allowed list")
}

func TestParseRSAPublicKey_TooSmall(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(t, err)
	n := base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())
	_, err = parseRSAPublicKey(n, e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too small")
}
