// pem.go holds pemBlock.

package config

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// pemBlock matches one PEM block.
var pemBlock = regexp.MustCompile(`-----BEGIN ([A-Z0-9 ]+)-----([\s\S]*?)-----END ([A-Z0-9 ]+)-----`)

// rewrapPEM writes each PEM block again with its base64 body in 64-column lines.
// A body that is not plain base64 stays as it is, so the parser reports it.
func rewrapPEM(v string) string {
	return pemBlock.ReplaceAllStringFunc(v, func(block string) string {
		m := pemBlock.FindStringSubmatch(block)
		body := strings.Join(strings.Fields(m[2]), "")
		if _, err := base64.StdEncoding.DecodeString(body); err != nil {
			return block
		}
		var b strings.Builder
		b.WriteString("-----BEGIN " + m[1] + "-----\n")
		for len(body) > 64 {
			b.WriteString(body[:64] + "\n")
			body = body[64:]
		}
		b.WriteString(body + "\n-----END " + m[3] + "-----")
		return b.String()
	})
}

// ParseGitHubAppKey resolves a BUILDHOST_GITHUB_APP_PRIVATE_KEY value (the PEM,
// or a path to it) and parses the RSA key.
func ParseGitHubAppKey(v string) (*rsa.PrivateKey, error) {
	v = strings.TrimSpace(v)
	pemText := resolvePEM(v)
	if !strings.Contains(pemText, "-----BEGIN") {
		return nil, fmt.Errorf("want the PEM text (-----BEGIN RSA PRIVATE KEY----- ...) or the path of a readable PEM file, got %q", short(v))
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(pemText))
	if err != nil {
		return nil, fmt.Errorf("not an RSA private key in PEM form: %w", err)
	}
	return key, nil
}

// KeyFingerprint is the fingerprint GitHub shows for an App private key: the
// base64 SHA-256 of the DER public key.
func KeyFingerprint(key *rsa.PrivateKey) string {
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(der)
	return "SHA256:" + base64.StdEncoding.EncodeToString(sum[:])
}

// short cuts a value for an error message, so that a pasted secret does not echo back.
func short(v string) string {
	if len(v) > 16 {
		return v[:16] + "..."
	}
	return v
}
