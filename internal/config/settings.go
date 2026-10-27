// Settings lists every BUILDHOST_* variable the server reads. The admin
// dashboard stores a value for any of them that is not EnvOnly. A stored value
// takes precedence over the environment, and the environment over the default.

package config

import (
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Kind says how a setting's value parses.
type Kind string

const (
	KindString   Kind = "string"
	KindSecret   Kind = "secret" // never sent back to the dashboard
	KindBool     Kind = "bool"
	KindDuration Kind = "duration"
	KindPercent  Kind = "percent"
	KindBytes    Kind = "bytes"
	KindInt      Kind = "int"
	KindList     Kind = "list" // comma-separated
)

// Setting describes one variable.
type Setting struct {
	Name        string `json:"name"`
	Group       string `json:"group"`
	Kind        Kind   `json:"kind"`
	Default     string `json:"default"`
	Description string `json:"description"`
	// EnvOnly settings are read before the database opens.
	EnvOnly bool `json:"env_only"`
	// Live settings are read on each use. The others apply at the next start.
	Live bool `json:"live"`
}

// Settings is every variable, in the order the dashboard shows them.
var Settings = []Setting{
	{Name: "BUILDHOST_PRIMARY_DOMAIN", Group: "Domains", Kind: KindString, Description: "The apex that owns the web UI, /api/v1 and the GitHub OAuth callback, or * to serve every Host. Required."},
	{Name: "BUILDHOST_SITE_DOMAIN", Group: "Domains", Kind: KindString, Description: "Serve each project's site at https://<project>.<domain>/. Empty adds no site routes."},
	{Name: "BUILDHOST_SITE_FETCH_DOMAINS", Group: "Domains", Kind: KindList, Description: "Domains a site page may fetch from."},

	{Name: "BUILDHOST_SECRET_SERVER_TOKEN", Group: "GitHub", Kind: KindSecret, Description: "secret-server machine token (sst_...) that vends the GitHub credential. github-state-mirror refuses a call with no credential, so run status, default branches and the Go proxy need one."},
	{Name: "BUILDHOST_GITHUB_TOKEN", Group: "GitHub", Kind: KindSecret, Description: "GitHub token for buildhost's own GitHub calls. Takes precedence over the secret-server token."},
	{Name: "BUILDHOST_GITHUB_APP_ID", Group: "GitHub", Kind: KindString, Description: "GitHub App id. Set with the private key. Takes precedence over the secret-server token."},
	{Name: "BUILDHOST_GITHUB_APP_PRIVATE_KEY", Group: "GitHub", Kind: KindSecret, Description: "GitHub App private key: the PEM, or a path to it."},
	{Name: "BUILDHOST_GITHUB_WEBHOOK_SECRET", Group: "GitHub", Kind: KindSecret, Description: "Secret that GitHub signs webhook deliveries with."},
	{Name: "BUILDHOST_GITHUB_CLIENT_ID", Group: "GitHub", Kind: KindString, Description: "OAuth client id for browser sign-in with GitHub."},
	{Name: "BUILDHOST_GITHUB_CLIENT_SECRET", Group: "GitHub", Kind: KindSecret, Description: "OAuth client secret for browser sign-in with GitHub."},

	{Name: "BUILDHOST_OIDC_ISSUERS", Group: "OIDC", Kind: KindList, Default: "https://token.actions.githubusercontent.com", Description: "Trusted OIDC token issuers."},
	{Name: "BUILDHOST_OIDC_ORGS", Group: "OIDC", Kind: KindList, Description: "GitHub orgs whose workflows may publish."},
	{Name: "BUILDHOST_OIDC_EVENTS", Group: "OIDC", Kind: KindList, Default: "push,pull_request,workflow_dispatch", Description: "Workflow events whose tokens may publish."},

	{Name: "BUILDHOST_GOPROXY_READINESS_MODULE", Group: "Go proxy", Kind: KindString, Description: "A private module the readiness check resolves. Without it the check can confirm only that a credential exists."},
	{Name: "BUILDHOST_GOPROXY_PRIVATE_PREFIXES", Group: "Go proxy", Kind: KindList, Description: "Module prefixes that count as private. Default: github.com/<org> for each OIDC org."},
	{Name: "BUILDHOST_GOPROXY_UPSTREAM", Group: "Go proxy", Kind: KindString, Description: "A public module mirror to pass misses to. Empty answers 404 so clients fall back to direct."},

	{Name: "BUILDHOST_RUN_FILES_KEEP", Group: "Janitors", Kind: KindDuration, Default: "24h", Description: "How long a run's files outlive the run."},
	{Name: "BUILDHOST_RUN_FILES_MIN_FREE_PERCENT", Group: "Janitors", Kind: KindPercent, Default: "10", Description: "Below this free disk share, ended runs' files go sooner."},
	{Name: "BUILDHOST_CACHE_KEEP", Group: "Janitors", Kind: KindDuration, Default: "168h", Description: "How long a cache entry nothing restores is kept."},
	{Name: "BUILDHOST_CACHE_MIN_FREE_PERCENT", Group: "Janitors", Kind: KindPercent, Default: "15", Description: "Below this free disk share, the least recently used cache entries go."},
	{Name: "BUILDHOST_MIRROR_KEEP", Group: "Janitors", Kind: KindDuration, Default: "168h", Description: "How long a mirrored blob nothing reads is kept."},
	{Name: "BUILDHOST_MIRROR_MIN_FREE_PERCENT", Group: "Janitors", Kind: KindPercent, Default: "15", Description: "Below this free disk share, the least recently read mirror blobs go."},

	{Name: "BUILDHOST_RETENTION_INTERVAL", Group: "Retention", Kind: KindDuration, Default: "0s", Description: "How often the retention sweeper runs. 0 disables it."},
	{Name: "BUILDHOST_RETENTION_ENFORCE", Group: "Retention", Kind: KindBool, Default: "false", Description: "Delete what retention evicts. False only reports."},
	{Name: "BUILDHOST_RETENTION_KEEP_N", Group: "Retention", Kind: KindInt, Default: "10", Description: "Seeds the retention policy on first start. The Retention page edits the live policy."},
	{Name: "BUILDHOST_RETENTION_RECENCY_GUARD", Group: "Retention", Kind: KindDuration, Default: "24h", Description: "Seeds the retention policy on first start. The Retention page edits the live policy."},

	{Name: "BUILDHOST_MAX_UPLOAD_SIZE", Group: "Uploads", Kind: KindBytes, Default: "2G", Live: true, Description: "Cap on one REST artifact upload."},
	{Name: "BUILDHOST_MAX_BLOB_SIZE", Group: "Uploads", Kind: KindBytes, Default: "10G", Live: true, Description: "Cap on one OCI blob."},
	{Name: "BUILDHOST_MAX_DIRECT_UPLOAD_SIZE", Group: "Uploads", Kind: KindBytes, Default: "95M", Live: true, Description: "The direct upload size the server advertises. Larger bodies go in chunks."},
	{Name: "BUILDHOST_UPLOAD_SESSION_TTL", Group: "Uploads", Kind: KindDuration, Default: "24h", Live: true, Description: "How long an idle chunked upload session lives."},

	{Name: "BUILDHOST_STORAGE_COMPRESS", Group: "Server", Kind: KindBool, Default: "true", Description: "zstd-compress stored blobs."},
	{Name: "BUILDHOST_OTEL_ENDPOINT", Group: "Server", Kind: KindString, Description: "OTLP/HTTP endpoint for traces and logs. Empty disables telemetry."},
	{Name: "BUILDHOST_LISTEN_ADDR", Group: "Server", Kind: KindString, Default: ":8080", EnvOnly: true, Description: "Main server address. The deployment's port wiring depends on it."},
	{Name: "BUILDHOST_ADMIN_LISTEN_ADDR", Group: "Server", Kind: KindString, Default: ":9090", EnvOnly: true, Description: "Admin dashboard address. Empty disables the dashboard."},
	{Name: "BUILDHOST_DATA_DIR", Group: "Server", Kind: KindString, Default: "./data", EnvOnly: true, Description: "Blob store and temp directory."},
	{Name: "BUILDHOST_DB_PATH", Group: "Server", Kind: KindString, Default: "./data/buildhost.db", EnvOnly: true, Description: "SQLite database. The stored settings live in it."},
}

// LookupSetting returns the setting named name.
func LookupSetting(name string) (Setting, bool) {
	for _, s := range Settings {
		if s.Name == name {
			return s, true
		}
	}
	return Setting{}, false
}

var stored atomic.Pointer[map[string]string]

// SetStored replaces the values the dashboard stored.
func SetStored(m map[string]string) {
	c := maps.Clone(m)
	stored.Store(&c)
}

// StoredValue returns the dashboard value for name.
func StoredValue(name string) (string, bool) {
	if m := stored.Load(); m != nil {
		v, ok := (*m)[name]
		return v, ok
	}
	return "", false
}

// LookupEnv is os.LookupEnv with the dashboard values in front.
func LookupEnv(name string) (string, bool) {
	if v, ok := StoredValue(name); ok {
		return v, true
	}
	return os.LookupEnv(name)
}

// Getenv is os.Getenv with the dashboard values in front.
func Getenv(name string) string {
	v, _ := LookupEnv(name)
	return v
}

var started atomic.Pointer[map[string]string]

// MarkStarted records the value of each setting the running server loaded.
func MarkStarted() {
	m := make(map[string]string, len(Settings))
	for _, s := range Settings {
		m[s.Name] = Getenv(s.Name)
	}
	started.Store(&m)
}

// RestartPending reports a setting whose value changed after the server loaded
// it. Such a value applies at the next start.
func RestartPending(s Setting) bool {
	m := started.Load()
	if s.Live || m == nil {
		return false
	}
	return (*m)[s.Name] != Getenv(s.Name)
}

// Source says where the value of name comes from: "dashboard", "environment" or "default".
func Source(name string) string {
	if _, ok := StoredValue(name); ok {
		return "dashboard"
	}
	if _, ok := os.LookupEnv(name); ok {
		return "environment"
	}
	return "default"
}

// CheckValue returns an error when value cannot be stored for setting s.
func CheckValue(s Setting, value string) error {
	if s.EnvOnly {
		return fmt.Errorf("%s is read from the environment only", s.Name)
	}
	v := strings.TrimSpace(value)
	if v == "" {
		return nil
	}
	switch s.Kind {
	case KindBool:
		if v != "true" && v != "false" && v != "1" && v != "0" {
			return fmt.Errorf("%s: want true or false", s.Name)
		}
	case KindDuration:
		if d, err := time.ParseDuration(v); err != nil || d < 0 {
			return fmt.Errorf("%s: want a non-negative Go duration, such as 24h", s.Name)
		}
	case KindPercent:
		if p, err := strconv.ParseFloat(v, 64); err != nil || p < 0 || p >= 100 {
			return fmt.Errorf("%s: want a number from 0 up to 100", s.Name)
		}
	case KindBytes:
		if n, err := ParseByteSize(v); err != nil || n <= 0 {
			return fmt.Errorf("%s: want a positive size, such as 95M", s.Name)
		}
	case KindInt:
		if n, err := strconv.Atoi(v); err != nil || n < 0 {
			return fmt.Errorf("%s: want a non-negative integer", s.Name)
		}
	}
	if s.Name == "BUILDHOST_SECRET_SERVER_TOKEN" && !strings.HasPrefix(v, "sst_") {
		return fmt.Errorf("%s: want a secret-server machine token, which starts with sst_", s.Name)
	}
	return nil
}
