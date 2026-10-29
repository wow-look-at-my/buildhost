package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateGitHubApp(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	goodPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))

	cases := []struct {
		name, id, key, wantErr string
	}{
		{name: "neither", id: "", key: ""},
		{name: "both", id: "123", key: goodPEM},
		{name: "id only", id: "123", key: "", wantErr: "BUILDHOST_GITHUB_APP_PRIVATE_KEY is empty"},
		{name: "key only", id: "", key: goodPEM, wantErr: "BUILDHOST_GITHUB_APP_ID is empty"},
		{name: "bad key", id: "123", key: "not a pem", wantErr: "not an RSA private key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{PrimaryDomain: "example.com", GitHubAppID: tc.id, GitHubAppPrivateKey: tc.key}
			err := c.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err, "a GitHub App credential that cannot work must stop the server at startup")
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestSourceEmptyEnvironmentIsDefault(t *testing.T) {
	t.Serial()
	SetStored(nil)
	t.Setenv("BUILDHOST_GITHUB_APP_ID", "")
	assert.Equal(t, "default", Source("BUILDHOST_GITHUB_APP_ID"), "an empty variable sets nothing")
	t.Setenv("BUILDHOST_GITHUB_APP_ID", "123")
	assert.Equal(t, "environment", Source("BUILDHOST_GITHUB_APP_ID"))
}
