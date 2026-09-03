package config

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultMaxUploadSize caps a single REST artifact upload (PUT .../artifacts).
	defaultMaxUploadSize int64 = 2 << 30
	// defaultMaxBlobSize caps a single OCI blob (image layer) pushed via the
	defaultMaxBlobSize int64 = 10 << 30
	// defaultMaxDirectUploadSize is the size the server ADVERTISES (via
	defaultMaxDirectUploadSize int64 = 95 << 20
	// defaultUploadSessionTTL is how long an in-progress chunked upload session
	defaultUploadSessionTTL = 24 * time.Hour
)

// MaxUploadSize is the cap for a single REST artifact upload, overridable via
func MaxUploadSize() int64 { return envBytes("BUILDHOST_MAX_UPLOAD_SIZE", defaultMaxUploadSize) }

// MaxBlobSize is the cap for a single OCI blob pushed to the registry endpoint,
func MaxBlobSize() int64 { return envBytes("BUILDHOST_MAX_BLOB_SIZE", defaultMaxBlobSize) }

// MaxDirectUploadSize is the advertised safe size for a single direct upload
func MaxDirectUploadSize() int64 {
	return envBytes("BUILDHOST_MAX_DIRECT_UPLOAD_SIZE", defaultMaxDirectUploadSize)
}

// UploadSessionTTL is how long an idle chunked upload session lives before it
func UploadSessionTTL() time.Duration {
	return envDuration("BUILDHOST_UPLOAD_SESSION_TTL", defaultUploadSessionTTL)
}

// envDuration parses a Go duration (e.g. "1h", "30m", "720h") from an env var,
// falling back to def on empty or invalid input.
func envDuration(name string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return def
	}
	return d
}

// envBytes parses a byte size from an env var, accepting a plain integer or an
// integer with a single-letter binary suffix (K, M, G, T). Invalid or
func envBytes(name string, def int64) int64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := ParseByteSize(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// ParseByteSize parses a byte size: a plain non-negative integer, or an
// integer with a single-letter binary suffix (K, M, G, T), e.g. "64M".
// Shared by the env-var config above and the CLI's --chunk-size flag.
func ParseByteSize(s string) (int64, error) {
	v := strings.TrimSpace(s)
	if v == "" {
		return 0, fmt.Errorf("empty size")
	}
	mult := int64(1)
	switch v[len(v)-1] {
	case 'k', 'K':
		mult = 1 << 10
	case 'm', 'M':
		mult = 1 << 20
	case 'g', 'G':
		mult = 1 << 30
	case 't', 'T':
		mult = 1 << 40
	}
	if mult != 1 {
		v = strings.TrimSpace(v[:len(v)-1])
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	if n > math.MaxInt64/mult {
		return 0, fmt.Errorf("size %q overflows", s)
	}
	return n * mult, nil
}

type Config struct {
	ListenAddr          string
	AdminListenAddr     string
	DataDir             string
	DBPath              string
	StorageCompress     bool
	OIDCIssuers         []string
	OIDCOrgs            []string
	OIDCEvents          []string
	GitHubWebhookSecret string
	// GitHub App credentials for buildhost's own REST lookups (resolving a repo's
	GitHubAppID         string
	GitHubAppPrivateKey string
	// GitHubToken is a static-PAT fallback for the same lookups when no App is
	GitHubToken      string
	OTELEndpoint     string
	SiteFetchDomains []string

	// SiteDomain is an optional dedicated domain for project static sites: when
	SiteDomain string
	// PrimaryDomain is the apex the GitHub OAuth callback is registered on (e.g.
	PrimaryDomain string

	// Sign in with GitHub (browser login for private resources). When the client
	GitHubClientID     string
	GitHubClientSecret string

	// Retention / garbage collection. Report-only by default: nothing is deleted
	RetentionKeepN        int // published releases kept per (project, branch)
	RetentionInterval     time.Duration
	RetentionRecencyGuard time.Duration // never evict releases newer than this
	RetentionEnforce      bool          // actually delete; false = report-only
}

// resolvePEM returns PEM contents from a config value that is either the PEM
// itself (contains a BEGIN marker) or a path to a PEM file. Inline PEM passed
func resolvePEM(v string) string {
	if strings.Contains(v, "-----BEGIN") {
		return unescapePEMNewlines(v)
	}
	if b, err := os.ReadFile(v); err == nil {
		return string(b)
	}
	return v
}

// unescapePEMNewlines turns the literal "\n" / "\r\n" escape sequences a
// multi-line secret picks up when squeezed through an environment variable back
func unescapePEMNewlines(v string) string {
	if strings.Contains(v, "\n") {
		return v
	}
	v = strings.ReplaceAll(v, `\r\n`, "\n")
	v = strings.ReplaceAll(v, `\n`, "\n")
	return v
}

// normalizeDomain canonicalizes a configured domain name: trimmed, lowercased,
func normalizeDomain(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.TrimPrefix(v, "*.")
	return strings.Trim(v, ".")
}

func Load() Config {
	c := Config{
		ListenAddr:      ":8080",
		AdminListenAddr: ":9090",
		DataDir:         "./data",
		DBPath:          "./data/buildhost.db",
		StorageCompress: true,
		OIDCIssuers:     []string{"https://token.actions.githubusercontent.com"},

		RetentionKeepN:        10,
		RetentionInterval:     0,
		RetentionRecencyGuard: 24 * time.Hour,
		RetentionEnforce:      false,
	}
	if v := os.Getenv("BUILDHOST_LISTEN_ADDR"); v != "" {
		c.ListenAddr = v
	}
	if v := os.Getenv("BUILDHOST_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("BUILDHOST_DB_PATH"); v != "" {
		c.DBPath = v
	}
	if v := os.Getenv("BUILDHOST_ADMIN_LISTEN_ADDR"); v != "" {
		c.AdminListenAddr = v
	}
	if v := os.Getenv("BUILDHOST_STORAGE_COMPRESS"); v == "false" || v == "0" {
		c.StorageCompress = false
	}
	if v := os.Getenv("BUILDHOST_OIDC_ISSUERS"); v != "" {
		c.OIDCIssuers = nil
		for _, iss := range strings.Split(v, ",") {
			if iss = strings.TrimSpace(iss); iss != "" {
				c.OIDCIssuers = append(c.OIDCIssuers, iss)
			}
		}
	}
	if v := os.Getenv("BUILDHOST_OIDC_ORGS"); v != "" {
		for _, org := range strings.Split(v, ",") {
			if org = strings.TrimSpace(org); org != "" {
				c.OIDCOrgs = append(c.OIDCOrgs, org)
			}
		}
	}
	if v := os.Getenv("BUILDHOST_OIDC_EVENTS"); v != "" {
		for _, ev := range strings.Split(v, ",") {
			if ev = strings.TrimSpace(ev); ev != "" {
				c.OIDCEvents = append(c.OIDCEvents, ev)
			}
		}
	}
	if len(c.OIDCEvents) == 0 {
		// workflow_dispatch is in the default set because GitHub only lets users
		c.OIDCEvents = []string{"push", "pull_request", "workflow_dispatch"}
	}
	if v := os.Getenv("BUILDHOST_GITHUB_WEBHOOK_SECRET"); v != "" {
		c.GitHubWebhookSecret = v
	}
	if v := os.Getenv("BUILDHOST_GITHUB_CLIENT_ID"); v != "" {
		c.GitHubClientID = v
	}
	if v := os.Getenv("BUILDHOST_GITHUB_CLIENT_SECRET"); v != "" {
		c.GitHubClientSecret = v
	}
	if v := strings.TrimSpace(os.Getenv("BUILDHOST_GITHUB_TOKEN")); v != "" {
		c.GitHubToken = v
	}
	if v := strings.TrimSpace(os.Getenv("BUILDHOST_GITHUB_APP_ID")); v != "" {
		c.GitHubAppID = v
	}
	if v := strings.TrimSpace(os.Getenv("BUILDHOST_GITHUB_APP_PRIVATE_KEY")); v != "" {
		c.GitHubAppPrivateKey = resolvePEM(v)
	}
	if v := os.Getenv("BUILDHOST_OTEL_ENDPOINT"); v != "" {
		c.OTELEndpoint = v
	}
	if v := os.Getenv("BUILDHOST_SITE_FETCH_DOMAINS"); v != "" {
		for _, d := range strings.Split(v, ",") {
			if d = strings.TrimSpace(d); d != "" {
				c.SiteFetchDomains = append(c.SiteFetchDomains, d)
			}
		}
	}
	if v := os.Getenv("BUILDHOST_SITE_DOMAIN"); v != "" {
		c.SiteDomain = normalizeDomain(v)
	}
	if v := os.Getenv("BUILDHOST_PRIMARY_DOMAIN"); v != "" {
		c.PrimaryDomain = normalizeDomain(v)
	}
	if v := os.Getenv("BUILDHOST_RETENTION_KEEP_N"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.RetentionKeepN = n
		}
	}
	c.RetentionInterval = envDuration("BUILDHOST_RETENTION_INTERVAL", c.RetentionInterval)
	c.RetentionRecencyGuard = envDuration("BUILDHOST_RETENTION_RECENCY_GUARD", c.RetentionRecencyGuard)
	if v := os.Getenv("BUILDHOST_RETENTION_ENFORCE"); v == "true" || v == "1" {
		c.RetentionEnforce = true
	}
	return c
}
