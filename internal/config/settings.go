// Settings lists every BUILDHOST_* variable the server reads. The admin
// dashboard stores a value for any of them that is not EnvOnly. A stored value
// takes precedence over the environment, and the environment over the default.

package config

import (
	"fmt"
	"maps"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Kind says how a setting's value parses.
type Kind string

const (
	KindString   Kind = "string"
	KindSecret   Kind = "secret" // never sent back to the dashboard
	KindPEM      Kind = "pem"    // a secret that the dashboard edits as multi-line text or a file
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
	// EnvOnly settings are read before the database opens. The dashboard cannot change them.
	EnvOnly bool `json:"env_only"`
}

// Secret reports a setting whose value never goes back to the dashboard.
func (s Setting) Secret() bool { return s.Kind == KindSecret || s.Kind == KindPEM }

// Settings is every variable, in the order the dashboard shows them.
var Settings = []Setting{
	{Name: "BUILDHOST_PRIMARY_DOMAIN", Group: "Domains", Kind: KindString, Description: "The apex that owns the web UI, /api/v1 and the GitHub OAuth callback, or * to serve every Host. Required."},
	{Name: "BUILDHOST_SITE_DOMAIN", Group: "Domains", Kind: KindString, Description: "Serve each project's site at https://<project>.<domain>/. Empty adds no site routes."},
	{Name: "BUILDHOST_SITE_FETCH_DOMAINS", Group: "Domains", Kind: KindList, Description: "Domains a site page may fetch from."},

	{Name: "BUILDHOST_SECRET_SERVER_TOKEN", Group: "GitHub", Kind: KindSecret, Description: "secret-server machine token (sst_...) that vends the GitHub credential. github-state-mirror refuses a call with no credential, so run status, default branches and the Go proxy need one."},
	{Name: "BUILDHOST_GITHUB_TOKEN", Group: "GitHub", Kind: KindSecret, Description: "GitHub token for buildhost's own GitHub calls. Takes precedence over the secret-server token."},
	{Name: "BUILDHOST_GITHUB_APP_ID", Group: "GitHub", Kind: KindString, Description: "GitHub App id. Set with the private key. Takes precedence over the secret-server token."},
	{Name: "BUILDHOST_GITHUB_APP_PRIVATE_KEY", Group: "GitHub", Kind: KindPEM, Description: "GitHub App private key: the PEM text, or a path to the PEM file. A save checks that the key parses."},
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

	{Name: "BUILDHOST_MAX_UPLOAD_SIZE", Group: "Uploads", Kind: KindBytes, Default: "2G", Description: "Cap on one REST artifact upload."},
	{Name: "BUILDHOST_MAX_BLOB_SIZE", Group: "Uploads", Kind: KindBytes, Default: "10G", Description: "Cap on one OCI blob."},
	{Name: "BUILDHOST_MAX_DIRECT_UPLOAD_SIZE", Group: "Uploads", Kind: KindBytes, Default: "95M", Description: "The direct upload size the server advertises. Larger bodies go in chunks."},
	{Name: "BUILDHOST_UPLOAD_SESSION_TTL", Group: "Uploads", Kind: KindDuration, Default: "24h", Description: "How long an idle chunked upload session lives."},

	{Name: "BUILDHOST_STORAGE_COMPRESS", Group: "Server", Kind: KindBool, Default: "true", Description: "zstd-compress stored blobs."},
	{Name: "BUILDHOST_OTEL_ENDPOINT", Group: "Server", Kind: KindString, Description: "OTLP/HTTP endpoint for traces and logs. Empty disables telemetry."},
	{Name: "BUILDHOST_LISTEN_ADDR", Group: "Server", Kind: KindString, Default: ":8080", EnvOnly: true, Description: "Main server address. The deployment's port wiring depends on it."},
	{Name: "BUILDHOST_ADMIN_LISTEN_ADDR", Group: "Server", Kind: KindString, Default: ":9090", EnvOnly: true, Description: "Admin dashboard address. Empty disables the dashboard."},
	{Name: "BUILDHOST_DATA_DIR", Group: "Server", Kind: KindString, Default: "./data", EnvOnly: true, Description: "Blob store and temp directory."},
	{Name: "BUILDHOST_DB_PATH", Group: "Server", Kind: KindString, Default: "./data/buildhost.db", EnvOnly: true, Description: "SQLite database. The stored settings live in it."},
}

// GitHubAppKeySetting is the GitHub App private key. The dashboard edits it as PEM text.
const GitHubAppKeySetting = "BUILDHOST_GITHUB_APP_PRIVATE_KEY"

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

var (
	current  atomic.Pointer[Config]
	changeMu sync.Mutex
	onChange []func(Config)
)

// Current returns the configuration in use. Reload replaces it.
func Current() Config {
	if c := current.Load(); c != nil {
		return *c
	}
	return Load()
}

// Reload loads the configuration again and gives it to each OnChange
// function, in the order they registered. Call it after SetStored.
func Reload() Config {
	changeMu.Lock()
	defer changeMu.Unlock()
	c := Load()
	current.Store(&c)
	for _, fn := range onChange {
		fn(c)
	}
	return c
}

// OnChange registers fn to receive the configuration on each Reload. fn must
// not call Reload or OnChange.
func OnChange(fn func(Config)) {
	changeMu.Lock()
	defer changeMu.Unlock()
	onChange = append(onChange, fn)
}

// CheckChange tests one change to the stored values and applies nothing. A nil
// value removes the stored value. The error says why the change cannot be
// saved. The warning names a saved state that does not work yet, such as half
// of a pair.
func CheckChange(name string, value *string) (warning string, err error) {
	s, ok := LookupSetting(name)
	if !ok {
		return "", fmt.Errorf("%s is not a setting", name)
	}
	if value != nil {
		if err := CheckValue(s, *value); err != nil {
			return "", err
		}
	} else if s.EnvOnly {
		return "", fmt.Errorf("%s is read from the environment only", s.Name)
	}
	c := loadWith(func(n string) (string, bool) {
		if n == name {
			if value != nil {
				return *value, true
			}
			return os.LookupEnv(n)
		}
		return LookupEnv(n)
	})
	if err := c.Validate(); err != nil {
		return "", err
	}
	if s.Group == "GitHub" {
		switch p := c.GitHubAppProblem(); {
		case p != "":
			warning = p
		case c.SecretServerToken != "" && (c.GitHubToken != "" || c.HasGitHubApp()):
			warning = "BUILDHOST_SECRET_SERVER_TOKEN is not used while BUILDHOST_GITHUB_TOKEN or the GitHub App is set"
		}
	}
	return warning, nil
}

// Source says where the value of name comes from: "dashboard", "environment" or "default".
func Source(name string) string {
	if _, ok := StoredValue(name); ok {
		return "dashboard"
	}
	// An empty variable sets nothing, so the page must not report it as a source.
	if v, ok := os.LookupEnv(name); ok && v != "" {
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
	if s.Name == GitHubAppKeySetting {
		if _, err := ParseGitHubAppKey(v); err != nil {
			return fmt.Errorf("%s: %w. Nothing was saved", s.Name, err)
		}
	}
	if s.Name == "BUILDHOST_SECRET_SERVER_TOKEN" && !strings.HasPrefix(v, "sst_") {
		return fmt.Errorf("%s: want a secret-server machine token, which starts with sst_", s.Name)
	}
	if check := formatChecks[s.Name]; check != nil {
		items := []string{v}
		if s.Kind == KindList {
			items = strings.Split(v, ",")
		}
		for _, item := range items {
			if item = strings.TrimSpace(item); item == "" {
				continue
			}
			if err := check(item); err != nil {
				return fmt.Errorf("%s: %w", s.Name, err)
			}
		}
	}
	return nil
}

// formatChecks holds the shape check for one value of a setting. For a list
// setting it checks each item.
var formatChecks = map[string]func(string) error{
	"BUILDHOST_PRIMARY_DOMAIN": func(v string) error {
		if v == PrimaryDomainAny {
			return nil
		}
		return checkDomain(v)
	},
	"BUILDHOST_SITE_DOMAIN":              checkDomain,
	"BUILDHOST_SITE_FETCH_DOMAINS":       checkDomain,
	"BUILDHOST_OIDC_ISSUERS":             checkURL,
	"BUILDHOST_OTEL_ENDPOINT":            checkURL,
	"BUILDHOST_GOPROXY_UPSTREAM":         checkURL,
	"BUILDHOST_GOPROXY_READINESS_MODULE": checkModulePath,
	"BUILDHOST_GOPROXY_PRIVATE_PREFIXES": checkModulePath,
}

// checkDomain accepts a host name or an IP address with no scheme, port or path.
func checkDomain(v string) error {
	d := normalizeDomain(v)
	if d == "" || len(d) > 253 {
		return fmt.Errorf("%q is not a domain name", v)
	}
	for _, label := range strings.Split(d, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("%q is not a domain name: want a bare host such as example.com, with no scheme, port or path", v)
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return fmt.Errorf("%q is not a domain name: want a bare host such as example.com, with no scheme, port or path", v)
			}
		}
	}
	return nil
}

func checkURL(v string) error {
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q is not a URL: want http:// or https:// and a host", v)
	}
	return nil
}

func checkModulePath(v string) error {
	if strings.Contains(v, "://") || strings.ContainsAny(v, " \t") || !strings.Contains(v, ".") {
		return fmt.Errorf("%q is not a module path: want a path such as github.com/org/repo, with no scheme", v)
	}
	return nil
}
