package config

import (
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultMaxUploadSize caps a single REST artifact upload (PUT .../artifacts).
	// It is a disk-fill guard, not a memory limit -- uploads stream to disk.
	defaultMaxUploadSize int64 = 2 << 30 // 2 GiB
	// defaultMaxBlobSize caps a single OCI blob (image layer) pushed via the
	// docker registry endpoint. Layers are streamed, so this is also just a
	// disk-fill guard; it is far larger than the REST cap because container
	// image layers (e.g. CUDA runtimes) routinely exceed 2 GiB.
	defaultMaxBlobSize int64 = 10 << 30 // 10 GiB
)

// MaxUploadSize is the cap for a single REST artifact upload, overridable via
// BUILDHOST_MAX_UPLOAD_SIZE (plain bytes, or with a K/M/G suffix).
func MaxUploadSize() int64 { return envBytes("BUILDHOST_MAX_UPLOAD_SIZE", defaultMaxUploadSize) }

// MaxBlobSize is the cap for a single OCI blob pushed to the registry endpoint,
// overridable via BUILDHOST_MAX_BLOB_SIZE (plain bytes, or with a K/M/G suffix).
func MaxBlobSize() int64 { return envBytes("BUILDHOST_MAX_BLOB_SIZE", defaultMaxBlobSize) }

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
// integer with a single-letter binary suffix (K, M, G, T). Invalid values fall
// back to def.
func envBytes(name string, def int64) int64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
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
	if err != nil || n <= 0 {
		return def
	}
	if n > math.MaxInt64/mult {
		return def // would overflow int64; ignore the bogus value
	}
	return n * mult
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
	// GitHubToken authenticates buildhost's own GitHub REST lookups (resolving a
	// repo's default branch for the apex "latest"). Optional: lookups fall back
	// to anonymous (60 req/hr/IP) when unset. From BUILDHOST_GITHUB_TOKEN.
	GitHubToken      string
	OTELEndpoint     string
	SiteFetchDomains []string

	// Retention / garbage collection. Report-only by default: nothing is deleted
	// unless RetentionEnforce is true. RetentionInterval == 0 disables the
	// background sweeper (the gc CLI still works on demand).
	RetentionKeepN        int           // published releases kept per (project, branch)
	RetentionInterval     time.Duration // background sweep cadence; 0 = disabled
	RetentionRecencyGuard time.Duration // never evict releases newer than this
	RetentionEnforce      bool          // actually delete; false = report-only
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
		c.OIDCEvents = []string{"push", "pull_request"}
	}
	if v := os.Getenv("BUILDHOST_GITHUB_WEBHOOK_SECRET"); v != "" {
		c.GitHubWebhookSecret = v
	}
	if v := strings.TrimSpace(os.Getenv("BUILDHOST_GITHUB_TOKEN")); v != "" {
		c.GitHubToken = v
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
