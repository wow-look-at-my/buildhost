// caBundles are where a Linux system keeps its CA bundle, the paths Go's crypto/x509 reads.

package mirror

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
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
var distrustedCAs = []string{"emudhra"}

// newClient talks to the upstream registries. It trusts the system CA bundle minus every distrusted CA.
func newClient() (*http.Client, error) {
	roots, err := trustedRoots()
	if err != nil {
		return nil, err
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = time.Minute
	t.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
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
