package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolvePEM must hand SetGitHubApp a parseable key no matter how the GitHub App
// private key was supplied: inline PEM with real newlines, inline PEM with the
// newlines escaped to the literal sequence "\n" (the usual shape after a
// multi-line secret is squeezed through an environment variable), or a file
// path. The escaped case is the one that was silently breaking App auth -- the
// key failed to parse, App auth was disabled, default-branch lookups fell back
// to anonymous and 404'd on private repos, so projects.default_branch never left
// the "master" seed.
func TestResolvePEM(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	// PKCS#1 ("RSA PRIVATE KEY") is the format GitHub issues App keys in.
	realPEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))

	// The same PEM as it commonly arrives through an env var: one line, newlines
	// escaped as the literal two-character sequence backslash-n.
	escaped := strings.ReplaceAll(strings.TrimRight(realPEM, "\n"), "\n", `\n`)
	require.NotContains(t, escaped, "\n", "escaped form must be single-line")

	parses := func(s string) bool {
		block, _ := pem.Decode([]byte(s))
		if block == nil {
			return false
		}
		_, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		return err == nil
	}
	require.False(t, parses(escaped), "guard: escaped PEM must not parse before resolvePEM")

	t.Run("inline real-newline PEM unchanged and parses", func(t *testing.T) {
		got := resolvePEM(realPEM)
		assert.Equal(t, realPEM, got)
		assert.True(t, parses(got))
	})

	t.Run("env-escaped inline PEM is un-escaped and parses", func(t *testing.T) {
		assert.True(t, parses(resolvePEM(escaped)), "App key must parse after resolvePEM un-escapes it")
	})

	t.Run("CRLF-escaped inline PEM is un-escaped and parses", func(t *testing.T) {
		crlf := strings.ReplaceAll(strings.TrimRight(realPEM, "\n"), "\n", `\r\n`)
		assert.True(t, parses(resolvePEM(crlf)))
	})

	t.Run("file path is read from disk", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "key.pem")
		require.NoError(t, os.WriteFile(p, []byte(realPEM), 0o600))
		assert.Equal(t, realPEM, resolvePEM(p))
	})

	t.Run("non-PEM, non-path value passes through unchanged", func(t *testing.T) {
		assert.Equal(t, "not-a-pem-or-path", resolvePEM("not-a-pem-or-path"))
	})
}
