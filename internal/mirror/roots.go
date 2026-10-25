// caBundles are where a Linux system keeps its CA bundle, the paths Go's crypto/x509 reads.

package mirror

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-containers/set"
)

// caBundles are where a Linux system keeps its CA bundle, the paths Go's crypto/x509 reads.
var caBundles = []string{
	"/etc/ssl/certs/ca-certificates.crt",
	"/etc/pki/tls/certs/ca-bundle.crt",
	"/etc/ssl/ca-bundle.pem",
	"/etc/pki/tls/cacert.pem",
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
	"/etc/ssl/cert.pem",
}

// distrustedCAs are certificate authorities the mirror never trusts, matched against subject and issuer.
var distrustedCAs = []string{"emudhra", "emsign"}

// compromisedCerts are the SHA-256 fingerprints of those eMudhra intermediates with recoverable keys.
// Sectigo's AAA Certificate Services also signed them, so a root filter alone does not stop them.
var compromisedCerts = set.Of(
	"e7568fe6e12331f4fd7de61da4c7559d0f8eb192a218d4741a3ba75184333838",
	"98b896625c0c230adeec3eb2cf6aad5001ecf9156a68644e82374ce5833bc25b",
	"42c4accd12c4f59597925e1a08a7a90102608543809308d50ec427e118ba1493",
	"432ec25f0d28de9006d97bf752f17bbb2cd41f669933fcb072d9a1148d0261f3",
	"cb6e15165ac802e9b3f993f16c25d41f4e81c21ebde71ae1b012ac00bf9c7c3f",
	"0164d7ceae5bb909c272af151ff44b1177ae9b47d9424b94f1d9558c30934d19",
	"9e39de2881273acc225a149c72ba3e0f51424963669ab633ee1dc7df5db067b4",
	"4b2460a466f95fd40113bcdcca2ceb29b3b093921de45474da7df48874c630b1",
)

// refuseDistrustedChains rejects a TLS connection when the peer presents, or a verified chain holds,
// a distrusted or compromised certificate. It catches a chain that ends at a root the pool still trusts.
func refuseDistrustedChains(cs tls.ConnectionState) error {
	certs := append([]*x509.Certificate{}, cs.PeerCertificates...)
	for _, chain := range cs.VerifiedChains {
		certs = append(certs, chain...)
	}
	for _, c := range certs {
		sum := sha256.Sum256(c.Raw)
		if distrusted(c) || compromisedCerts.Contains(hex.EncodeToString(sum[:])) {
			return fmt.Errorf("refusing a TLS chain that holds %q: eMudhra keys are compromised", c.Subject.String())
		}
	}
	return nil
}

// newClient talks to the upstream registries. It trusts the system CA bundle minus every distrusted CA.
func newClient() (*http.Client, error) {
	roots, err := trustedRoots()
	if err != nil {
		return nil, err
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = time.Minute
	t.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12, VerifyConnection: refuseDistrustedChains}
	return &http.Client{Transport: t}, nil
}

// trustedRoots reads the system CA bundle. SSL_CERT_FILE wins, as it does for Go itself.
func trustedRoots() (*x509.CertPool, error) {
	paths := caBundles
	if f := os.Getenv("SSL_CERT_FILE"); f != "" {
		paths = []string{f}
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil || len(b) == 0 {
			continue
		}
		pool, err := poolWithoutDistrusted(b)
		if err != nil {
			return nil, fmt.Errorf("mirror: CA bundle %s: %w", p, err)
		}
		return pool, nil
	}
	return nil, fmt.Errorf("mirror: no CA bundle at %s; the mirror cannot verify an upstream registry without one", strings.Join(paths, ", "))
}

// poolWithoutDistrusted builds a pool from a PEM bundle, without any certificate a distrusted CA owns or issued.
func poolWithoutDistrusted(bundle []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	kept := 0
	for rest := bundle; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		if distrusted(cert) {
			continue
		}
		pool.AddCert(cert)
		kept++
	}
	if kept == 0 {
		return nil, fmt.Errorf("no trusted certificate is left")
	}
	return pool, nil
}

func distrusted(cert *x509.Certificate) bool {
	names := strings.ToLower(cert.Subject.String() + "\n" + cert.Issuer.String())
	for _, ca := range distrustedCAs {
		if strings.Contains(names, ca) {
			return true
		}
	}
	return false
}
