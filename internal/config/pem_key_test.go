package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKeyPEM(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func TestParseGitHubAppKey(t *testing.T) {
	key, real := testKeyPEM(t)

	// A single-line HTML input removes every line break from a paste.
	flat := strings.ReplaceAll(real, "\n", "")
	got, err := ParseGitHubAppKey(flat)
	require.NoError(t, err, "a PEM with its line breaks removed must parse")
	assert.True(t, key.Equal(got))

	spaced := strings.ReplaceAll(real, "\n", " ")
	_, err = ParseGitHubAppKey(spaced)
	require.NoError(t, err, "a PEM with its line breaks folded to spaces must parse")

	_, err = ParseGitHubAppKey("  \n" + real + "\n\n")
	require.NoError(t, err)

	p := filepath.Join(t.TempDir(), "key.pem")
	require.NoError(t, os.WriteFile(p, []byte(flat), 0o600))
	_, err = ParseGitHubAppKey(p)
	require.NoError(t, err, "a path to a PEM file must parse")

	_, err = ParseGitHubAppKey("ghp_notakeyatallreally")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "want the PEM text")
	assert.NotContains(t, err.Error(), "notakeyatallreally", "the error must not echo the whole value")

	broken := strings.Replace(real, "MII", "XXX", 1)
	_, err = ParseGitHubAppKey(broken)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an RSA private key")
}

// KeyFingerprint must match what GitHub shows:
// openssl rsa -pubout -outform DER | openssl sha256 -binary | openssl base64.
func TestKeyFingerprint(t *testing.T) {
	key, _ := testKeyPEM(t)
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	sum := sha256.Sum256(der)
	assert.Equal(t, "SHA256:"+base64.StdEncoding.EncodeToString(sum[:]), KeyFingerprint(key))
}

func TestCheckValueRefusesABadAppKey(t *testing.T) {
	st, ok := LookupSetting(GitHubAppKeySetting)
	require.True(t, ok)
	assert.True(t, st.Secret())
	require.Error(t, CheckValue(st, "-----BEGIN RSA PRIVATE KEY-----garbage-----END RSA PRIVATE KEY-----"))
	_, real := testKeyPEM(t)
	require.NoError(t, CheckValue(st, real))
	require.NoError(t, CheckValue(st, ""), "an empty value is an explicit empty override")
}
