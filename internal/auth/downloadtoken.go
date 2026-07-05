package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// downloadTokenPrefix tags a signed, artifact-bound temporary download token so
// it is unmistakable for a regular bh_ API token (LookupToken never matches it)
// and so VerifyDownloadToken rejects anything that is not one of ours.
const downloadTokenPrefix = "bhdl_"

var (
	downloadSecretMu sync.Mutex
	downloadSecret   []byte
)

// initDownloadSecret loads (or, on first start, generates) the HMAC key used to
// sign temporary download links. It lives next to the APT signing key in the
// data dir so links survive restarts. Called once from Init.
func initDownloadSecret(dataDir string) {
	setDownloadSecret(loadOrCreateDownloadSecret(filepath.Join(dataDir, "download-signing.key")))
}

func setDownloadSecret(b []byte) {
	downloadSecretMu.Lock()
	downloadSecret = b
	downloadSecretMu.Unlock()
}

// downloadSecretBytes returns the signing key, lazily generating an ephemeral
// in-memory key if Init has not run (e.g. a unit test that mints and verifies in
// the same process). A persisted key is always preferred so issued links keep
// working across restarts.
func downloadSecretBytes() []byte {
	downloadSecretMu.Lock()
	defer downloadSecretMu.Unlock()
	if downloadSecret == nil {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			panic("auth: cannot generate download signing key: " + err.Error())
		}
		downloadSecret = b
	}
	return downloadSecret
}

func loadOrCreateDownloadSecret(path string) []byte {
	if data, err := os.ReadFile(path); err == nil && len(data) >= 32 {
		return data
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		slog.Error("download signing key generation failed", "err", err)
		return nil // downloadSecretBytes will lazily regenerate
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		slog.Error("download signing key dir create failed", "err", err)
		return b // usable this process, just not persisted
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		slog.Error("download signing key write failed", "err", err)
		return b
	}
	slog.Info("generated download signing key", "path", path)
	return b
}

// MintDownloadToken returns a signed, expiring token authorizing exactly one
// artifact download: the (project, version, os, arch, fmt, debug) tuple. It is
// delivered as the &token= query param on a static.{domain}/file URL and lets an
// otherwise-private artifact be fetched until exp without a project token. The
// resource tuple is bound by the signature, so a leaked link exposes only that
// one file, and only until it expires.
func MintDownloadToken(project, version, osStr, archStr, fmtStr string, debug bool, exp time.Time) string {
	expUnix := exp.Unix()
	mac := downloadMAC(project, version, osStr, archStr, fmtStr, debug, expUnix)
	buf := make([]byte, 8+len(mac))
	binary.BigEndian.PutUint64(buf[:8], uint64(expUnix))
	copy(buf[8:], mac)
	return downloadTokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
}

// VerifyDownloadToken reports whether token is a valid, unexpired signature for
// exactly this artifact tuple. Any field mismatch, tamper, malformed token, or
// past expiry returns false.
func VerifyDownloadToken(token, project, version, osStr, archStr, fmtStr string, debug bool) bool {
	if !strings.HasPrefix(token, downloadTokenPrefix) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(token[len(downloadTokenPrefix):])
	if err != nil || len(raw) != 8+sha256.Size {
		return false
	}
	expUnix := int64(binary.BigEndian.Uint64(raw[:8]))
	if time.Now().Unix() > expUnix {
		return false
	}
	want := downloadMAC(project, version, osStr, archStr, fmtStr, debug, expUnix)
	return hmac.Equal(raw[8:], want)
}

func downloadMAC(project, version, osStr, archStr, fmtStr string, debug bool, expUnix int64) []byte {
	debugStr := "0"
	if debug {
		debugStr = "1"
	}
	// NUL-joined: project/os/arch/fmt are charset-validated and never contain
	// NUL, so fields cannot run together to forge a different tuple.
	msg := strings.Join([]string{project, version, osStr, archStr, fmtStr, debugStr, strconv.FormatInt(expUnix, 10)}, "\x00")
	h := hmac.New(sha256.New, downloadSecretBytes())
	h.Write([]byte(msg))
	return h.Sum(nil)
}

// knownServiceLabels are the first-Host-label names the server treats as service
// subdomains, plus the admin dashboard host. Used to derive the registry apex
// from any request Host.
var knownServiceLabels = map[string]bool{
	"apt": true, "brew": true, "dl": true, "git": true, "npm": true,
	"oci": true, "sites": true, "static": true, "docker": true, "admin": true,
}

// ApexServiceURL returns scheme://<service>.<apex>, deriving the apex from the
// request Host by stripping a known leading service/admin label. Unlike
// DeriveServiceURL -- which strips the first label unconditionally and is only
// correct when called from a service subdomain -- this is also correct from the
// apex, so it works for both the apex REST API and the admin subdomain.
func ApexServiceURL(r *http.Request, service string) *url.URL {
	host, port := r.Host, ""
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host, port = host[:i], host[i:]
	}
	if dot := strings.IndexByte(host, '.'); dot > 0 && knownServiceLabels[host[:dot]] {
		host = host[dot+1:]
	}
	return &url.URL{Scheme: RequestScheme(r), Host: service + "." + host + port}
}
