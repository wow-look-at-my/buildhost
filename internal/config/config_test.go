package config

import (
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestLoad_Defaults(t *testing.T) {
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
	t.Setenv("BUILDHOST_LISTEN_ADDR", ":9090")
	t.Setenv("BUILDHOST_DATA_DIR", "")
	t.Setenv("BUILDHOST_DB_PATH", "")

	c := Load()
	assert.Equal(t, ":9090", c.ListenAddr)
	assert.Equal(t, "./data", c.DataDir)
}

func TestLoad_DataDirOverride(t *testing.T) {
	t.Setenv("BUILDHOST_LISTEN_ADDR", "")
	t.Setenv("BUILDHOST_DATA_DIR", "/tmp/mydata")
	t.Setenv("BUILDHOST_DB_PATH", "")

	c := Load()
	assert.Equal(t, "/tmp/mydata", c.DataDir)
	assert.Equal(t, ":8080", c.ListenAddr)
}

func TestLoad_DBPathOverride(t *testing.T) {
	t.Setenv("BUILDHOST_LISTEN_ADDR", "")
	t.Setenv("BUILDHOST_DATA_DIR", "")
	t.Setenv("BUILDHOST_DB_PATH", "/var/lib/buildhost.db")

	c := Load()
	assert.Equal(t, "/var/lib/buildhost.db", c.DBPath)
}

func TestLoad_AllOverrides(t *testing.T) {
	t.Setenv("BUILDHOST_LISTEN_ADDR", "0.0.0.0:443")
	t.Setenv("BUILDHOST_DATA_DIR", "/opt/data")
	t.Setenv("BUILDHOST_DB_PATH", "/opt/data/prod.db")

	c := Load()
	assert.Equal(t, "0.0.0.0:443", c.ListenAddr)
	assert.Equal(t, "/opt/data", c.DataDir)
	assert.Equal(t, "/opt/data/prod.db", c.DBPath)
}

func TestLoad_OIDCIssuers(t *testing.T) {
	t.Setenv("BUILDHOST_OIDC_ISSUERS", "https://issuer1.example.com, https://issuer2.example.com")

	c := Load()
	assert.Equal(t, []string{"https://issuer1.example.com", "https://issuer2.example.com"}, c.OIDCIssuers)
}

func TestLoad_OIDCOrgs(t *testing.T) {
	t.Setenv("BUILDHOST_OIDC_ORGS", "myorg, otherorg")

	c := Load()
	assert.Equal(t, []string{"myorg", "otherorg"}, c.OIDCOrgs)
}

func TestLoad_OIDCEvents_Custom(t *testing.T) {
	t.Setenv("BUILDHOST_OIDC_EVENTS", "push, workflow_dispatch")

	c := Load()
	assert.Equal(t, []string{"push", "workflow_dispatch"}, c.OIDCEvents)
}

func TestLoad_OIDCEvents_Default(t *testing.T) {
	t.Setenv("BUILDHOST_OIDC_EVENTS", "")

	c := Load()
	assert.Equal(t, []string{"push", "pull_request"}, c.OIDCEvents)
}

func TestEnvBytes(t *testing.T) {
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
		{"0", 77, 77},         // zero -> default
		{"G", 77, 77},         // suffix only -> default
		{"99999999999T", 77, 77}, // would overflow int64 -> default
	}
	for _, c := range cases {
		t.Setenv("BUILDHOST_TEST_BYTES", c.in)
		assert.Equal(t, c.want, envBytes("BUILDHOST_TEST_BYTES", c.def), "in=%q", c.in)
	}
}

func TestMaxSizes(t *testing.T) {
	t.Setenv("BUILDHOST_MAX_BLOB_SIZE", "")
	assert.Equal(t, defaultMaxBlobSize, MaxBlobSize())
	t.Setenv("BUILDHOST_MAX_BLOB_SIZE", "3G")
	assert.Equal(t, int64(3<<30), MaxBlobSize())

	t.Setenv("BUILDHOST_MAX_UPLOAD_SIZE", "")
	assert.Equal(t, defaultMaxUploadSize, MaxUploadSize())
	t.Setenv("BUILDHOST_MAX_UPLOAD_SIZE", "500M")
	assert.Equal(t, int64(500<<20), MaxUploadSize())
}
