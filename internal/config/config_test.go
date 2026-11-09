package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoad_Defaults(t *testing.T) {
	t.Serial()
	// Clear any env vars that could interfere.
	for _, key := range []string{
		"BUILDHOST_LISTEN_ADDR",
		"BUILDHOST_DATA_DIR",
		"BUILDHOST_DB_PATH",
	} {
		t.Setenv(key, "")
	}

	c := Load()
	assert.Equal(t, ":8080", c.ListenAddr)
	assert.Equal(t, "./data", c.DataDir)
	assert.Equal(t, "./data/buildhost.db", c.DBPath)
}

func TestLoad_ListenAddrOverride(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_LISTEN_ADDR", ":9090")
	t.Setenv("BUILDHOST_DATA_DIR", "")
	t.Setenv("BUILDHOST_DB_PATH", "")

	c := Load()
	assert.Equal(t, ":9090", c.ListenAddr)
	assert.Equal(t, "./data", c.DataDir)
}

// serve runs no admin server when this is empty. The e2e suites set it empty to
// share a runner, got the default port anyway, and died on a bound address.
func TestLoad_AdminListenAddrEmptyDisables(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_ADMIN_LISTEN_ADDR", "")

	c := Load()
	assert.Empty(t, c.AdminListenAddr)
}

func TestLoad_AdminListenAddrUnsetKeepsDefault(t *testing.T) {
	t.Serial()
	os.Unsetenv("BUILDHOST_ADMIN_LISTEN_ADDR")

	c := Load()
	assert.Equal(t, ":9090", c.AdminListenAddr)
}

func TestLoad_DataDirOverride(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_LISTEN_ADDR", "")
	t.Setenv("BUILDHOST_DATA_DIR", "/tmp/mydata")
	t.Setenv("BUILDHOST_DB_PATH", "")

	c := Load()
	assert.Equal(t, "/tmp/mydata", c.DataDir)
	assert.Equal(t, ":8080", c.ListenAddr)
}

func TestLoad_DBPathOverride(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_LISTEN_ADDR", "")
	t.Setenv("BUILDHOST_DATA_DIR", "")
	t.Setenv("BUILDHOST_DB_PATH", "/var/lib/buildhost.db")

	c := Load()
	assert.Equal(t, "/var/lib/buildhost.db", c.DBPath)
}

func TestLoad_AllOverrides(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_LISTEN_ADDR", "0.0.0.0:443")
	t.Setenv("BUILDHOST_DATA_DIR", "/opt/data")
	t.Setenv("BUILDHOST_DB_PATH", "/opt/data/prod.db")

	c := Load()
	assert.Equal(t, "0.0.0.0:443", c.ListenAddr)
	assert.Equal(t, "/opt/data", c.DataDir)
	assert.Equal(t, "/opt/data/prod.db", c.DBPath)
}

func TestLoad_OIDCIssuers(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_OIDC_ISSUERS", "https://issuer1.example.com, https://issuer2.example.com")

	c := Load()
	assert.Equal(t, []string{"https://issuer1.example.com", "https://issuer2.example.com"}, c.OIDCIssuers)
}

func TestLoad_OIDCOrgs(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_OIDC_ORGS", "myorg, otherorg")

	c := Load()
	assert.Equal(t, []string{"myorg", "otherorg"}, c.OIDCOrgs)
}

func TestLoad_OIDCEvents_Custom(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_OIDC_EVENTS", "push, workflow_dispatch")

	c := Load()
	assert.Equal(t, []string{"push", "workflow_dispatch"}, c.OIDCEvents)
}

func TestLoad_OIDCEvents_Default(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_OIDC_EVENTS", "")

	c := Load()
	assert.Equal(t, []string{"push", "pull_request", "workflow_dispatch"}, c.OIDCEvents)
}

func TestLoad_GitHubWebhookSecret(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_GITHUB_WEBHOOK_SECRET", "secret")

	c := Load()
	assert.Equal(t, "secret", c.GitHubWebhookSecret)
}

func TestEnvBytes(t *testing.T) {
	t.Serial()
	cases := []struct {
		in   string
		def  int64
		want int64
	}{
		{"", 100, 100},        // unset -> default
		{"   ", 100, 100},     // blank -> default
		{"500", 1, 500},       // plain bytes
		{"8K", 1, 8 << 10},    // upper suffix
		{"8k", 1, 8 << 10},    // lower suffix
		{"4M", 1, 4 << 20},    // mega
		{"2G", 1, 2 << 30},    // giga
		{"1T", 1, 1 << 40},    // tera
		{"  3G ", 1, 3 << 30}, // surrounding space
		{"bogus", 77, 77},     // unparseable -> default
		{"-5", 77, 77},        // non-positive -> default
		{"0", 77, 77},
		{"G", 77, 77},            // suffix only -> default
		{"99999999999T", 77, 77}, // would overflow int64 -> default
	}
	for _, c := range cases {
		t.Setenv("BUILDHOST_TEST_BYTES", c.in)
		assert.Equal(t, c.want, envBytes("BUILDHOST_TEST_BYTES", c.def), "in=%q", c.in)
	}
}

func TestMaxSizes(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_MAX_BLOB_SIZE", "")
	assert.Equal(t, defaultMaxBlobSize, MaxBlobSize())
	t.Setenv("BUILDHOST_MAX_BLOB_SIZE", "3G")
	assert.Equal(t, int64(3<<30), MaxBlobSize())

	t.Setenv("BUILDHOST_MAX_UPLOAD_SIZE", "")
	assert.Equal(t, defaultMaxUploadSize, MaxUploadSize())
	t.Setenv("BUILDHOST_MAX_UPLOAD_SIZE", "500M")
	assert.Equal(t, int64(500<<20), MaxUploadSize())
}

// The scratch root is TMPDIR unless a dedicated setting names one.
func TestLoad_TempDirDefaultsToTmpdir(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_TEMP_DIR", "")
	t.Setenv("TMPDIR", "/custom/tmpdir")

	assert.Equal(t, "/custom/tmpdir", Load().TempDir)
}

func TestLoad_TempDirOverride(t *testing.T) {
	t.Serial()
	t.Setenv("TMPDIR", "/custom/tmpdir")
	t.Setenv("BUILDHOST_TEMP_DIR", "/var/scratch")

	assert.Equal(t, "/var/scratch", Load().TempDir)
}

func TestLoad_RecompressDefaults(t *testing.T) {
	t.Serial()
	for _, name := range []string{
		"BUILDHOST_RECOMPRESS_CACHE_SIZE",
		"BUILDHOST_RECOMPRESS_CACHE_ENTRIES",
		"BUILDHOST_RECOMPRESS_CACHE_KEEP",
		"BUILDHOST_TEMP_STALE_AGE",
		"BUILDHOST_RECOMPRESS_PER_IP",
		"BUILDHOST_RECOMPRESS_WINDOW",
		"BUILDHOST_RECOMPRESS_LOAD_CEILING",
	} {
		t.Setenv(name, "")
	}

	c := Load()
	assert.Equal(t, int64(512<<20), c.RecompressCacheLimit)
	assert.Equal(t, 512, c.RecompressEntryLimit)
	assert.Equal(t, time.Hour, c.RecompressTTL)
	assert.Equal(t, time.Hour, c.ScratchAge)
	assert.Equal(t, 20, c.RecompressPerIP)
	assert.Equal(t, time.Minute, c.RecompressWindow)
	assert.Equal(t, 1.0, c.RecompressLoadCeiling)
	t.Setenv("BUILDHOST_PRIMARY_DOMAIN", "example.com")
	assert.NoError(t, Load().Validate())
}

func TestLoad_RecompressOverrides(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_RECOMPRESS_CACHE_SIZE", "64M")
	t.Setenv("BUILDHOST_RECOMPRESS_CACHE_ENTRIES", "8")
	t.Setenv("BUILDHOST_RECOMPRESS_CACHE_KEEP", "30m")
	t.Setenv("BUILDHOST_TEMP_STALE_AGE", "15m")
	t.Setenv("BUILDHOST_RECOMPRESS_PER_IP", "1")
	t.Setenv("BUILDHOST_RECOMPRESS_WINDOW", "10s")
	t.Setenv("BUILDHOST_RECOMPRESS_LOAD_CEILING", "0.75")

	c := Load()
	assert.Equal(t, int64(64<<20), c.RecompressCacheLimit)
	assert.Equal(t, 8, c.RecompressEntryLimit)
	assert.Equal(t, 30*time.Minute, c.RecompressTTL)
	assert.Equal(t, 15*time.Minute, c.ScratchAge)
	assert.Equal(t, 1, c.RecompressPerIP)
	assert.Equal(t, 10*time.Second, c.RecompressWindow)
	assert.Equal(t, 0.75, c.RecompressLoadCeiling)
	t.Setenv("BUILDHOST_PRIMARY_DOMAIN", "example.com")
	assert.NoError(t, Load().Validate())
}

// A setting that is set and does not parse stops the server at startup rather
// than quietly reverting to a default.
func TestLoad_InvalidDownloadSettingsFailValidation(t *testing.T) {
	t.Serial()
	for _, tt := range []struct{ name, value string }{
		{"BUILDHOST_RECOMPRESS_CACHE_SIZE", "lots"},
		{"BUILDHOST_RECOMPRESS_CACHE_ENTRIES", "-1"},
		{"BUILDHOST_RECOMPRESS_CACHE_KEEP", "soon"},
		{"BUILDHOST_TEMP_STALE_AGE", "never"},
		{"BUILDHOST_RECOMPRESS_PER_IP", "many"},
		{"BUILDHOST_RECOMPRESS_WINDOW", "an hour"},
		{"BUILDHOST_RECOMPRESS_LOAD_CEILING", "high"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.name, tt.value)
			t.Setenv("BUILDHOST_PRIMARY_DOMAIN", "example.com")
			assert.Error(t, Load().Validate())
		})
	}
}
