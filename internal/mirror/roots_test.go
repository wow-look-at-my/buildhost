package mirror

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureCerts parses every certificate in a testdata file.
func fixtureCerts(t *testing.T, name string) []*x509.Certificate {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	var out []*x509.Certificate
	for rest := b; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return out
		}
		c, err := x509.ParseCertificate(block.Bytes)
		require.NoError(t, err)
		out = append(out, c)
	}
}

// The fixture holds eMudhra's real emSign roots from Mozilla's bundle, and ISRG Root X1.
func TestTrustedRootsDropTheRealEMudhraRoots(t *testing.T) {
	t.Setenv("SSL_CERT_FILE", filepath.Join("testdata", "emudhra-roots.pem"))
	pool, err := trustedRoots()
	require.NoError(t, err)
	certs := fixtureCerts(t, "emudhra-roots.pem")
	require.Len(t, certs, 5)
	for _, c := range certs {
		_, err := c.Verify(x509.VerifyOptions{Roots: pool})
		if strings.HasPrefix(c.Subject.CommonName, "emSign") {
			assert.Error(t, err, "%s must not be trusted", c.Subject.CommonName)
			continue
		}
		assert.NoError(t, err, "%s stays trusted", c.Subject.CommonName)
	}
}

func TestTrustedRootsRefuseABundleOfOnlyDistrustedCAs(t *testing.T) {
	var emSign []byte
	for _, c := range fixtureCerts(t, "emudhra-roots.pem") {
		if strings.HasPrefix(c.Subject.CommonName, "emSign") {
			emSign = append(emSign, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
		}
	}
	path := filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(path, emSign, 0o644))
	t.Setenv("SSL_CERT_FILE", path)
	_, err := trustedRoots()
	assert.ErrorContains(t, err, "no trusted certificate")
}

// Sectigo's AAA root cross-signed it, so a pool without eMudhra roots can still verify a
// chain through it.
func TestAChainThroughTheCompromisedIntermediateIsRefused(t *testing.T) {
	intermediate := fixtureCerts(t, "em-ov-tls-ca-g3c.pem")[0]
	sum := sha256.Sum256(intermediate.Raw)
	assert.True(t, compromisedCerts[hex.EncodeToString(sum[:])], "the fingerprint list must hold the real certificate")

	err := refuseDistrustedChains(tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{intermediate}}})
	assert.ErrorContains(t, err, "EM OV TLS CA - G3C")
	err = refuseDistrustedChains(tls.ConnectionState{PeerCertificates: []*x509.Certificate{intermediate}})
	assert.Error(t, err, "a presented certificate is checked as well as the verified chains")

	isrg := fixtureCerts(t, "emudhra-roots.pem")[4]
	assert.NoError(t, refuseDistrustedChains(tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{isrg}}}))
}

func TestTheMirrorClientRefusesDistrustedChains(t *testing.T) {
	t.Setenv("SSL_CERT_FILE", filepath.Join("testdata", "emudhra-roots.pem"))
	c, err := newClient()
	require.NoError(t, err)
	cfg := c.Transport.(*http.Transport).TLSClientConfig
	require.NotNil(t, cfg.VerifyConnection, "the client must check every chain, not only the pool")
}

func TestTrustedRootsFailWithoutABundle(t *testing.T) {
	t.Setenv("SSL_CERT_FILE", filepath.Join(t.TempDir(), "missing.crt"))
	_, err := newClient()
	assert.ErrorContains(t, err, "no CA bundle")
}

func TestAMirrorWithoutAClientFailsEveryPullLoudly(t *testing.T) {
	f := newFixture(t)
	f.h.Client = nil
	f.h.clientErr = assert.AnError
	rec := f.do(t, "GET", "/v2/library/alpine/manifests/latest", nil, true)
	assert.Equal(t, 502, rec.Code, "a mirror that cannot verify its upstream must say so, not pretend the image is absent")
	assert.Contains(t, rec.Body.String(), assert.AnError.Error())
}
