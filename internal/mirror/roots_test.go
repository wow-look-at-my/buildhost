package mirror

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// selfSigned is a CA certificate whose subject names org, and its PEM.
func selfSigned(t *testing.T, org string) (*x509.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{org}, CommonName: org + " Root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestTrustedRootsDropEMudhra(t *testing.T) {
	good, goodPEM := selfSigned(t, "Trusted Test CA")
	bad, badPEM := selfSigned(t, "eMudhra Technologies Limited")
	path := filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(path, []byte(goodPEM+badPEM), 0o644))
	t.Setenv("SSL_CERT_FILE", path)

	pool, err := trustedRoots()
	require.NoError(t, err)
	_, err = good.Verify(x509.VerifyOptions{Roots: pool})
	assert.NoError(t, err, "a CA in the system bundle stays trusted")
	_, err = bad.Verify(x509.VerifyOptions{Roots: pool})
	assert.Error(t, err, "the mirror must never trust an eMudhra certificate")
}

func TestTrustedRootsRefuseABundleOfOnlyDistrustedCAs(t *testing.T) {
	_, badPEM := selfSigned(t, "eMudhra Technologies Limited")
	path := filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(path, []byte(badPEM), 0o644))
	t.Setenv("SSL_CERT_FILE", path)
	_, err := trustedRoots()
	assert.ErrorContains(t, err, "no trusted certificate")
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
