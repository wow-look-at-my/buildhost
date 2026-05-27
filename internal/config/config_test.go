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
		"BUILDHOST_BASE_URL",
	} {
		t.Setenv(key, "")
	}

	c := Load()
	assert.Equal(t, ":8080", c.ListenAddr)
	assert.Equal(t, "./data", c.DataDir)
	assert.Equal(t, "./data/buildhost.db", c.DBPath)
	assert.Equal(t, "http://localhost:8080", c.BaseURL)
}

func TestLoad_ListenAddrOverride(t *testing.T) {
	t.Setenv("BUILDHOST_LISTEN_ADDR", ":9090")
	t.Setenv("BUILDHOST_DATA_DIR", "")
	t.Setenv("BUILDHOST_DB_PATH", "")
	t.Setenv("BUILDHOST_BASE_URL", "")

	c := Load()
	assert.Equal(t, ":9090", c.ListenAddr)
	assert.Equal(t, "./data", c.DataDir)
}

func TestLoad_DataDirOverride(t *testing.T) {
	t.Setenv("BUILDHOST_LISTEN_ADDR", "")
	t.Setenv("BUILDHOST_DATA_DIR", "/tmp/mydata")
	t.Setenv("BUILDHOST_DB_PATH", "")
	t.Setenv("BUILDHOST_BASE_URL", "")

	c := Load()
	assert.Equal(t, "/tmp/mydata", c.DataDir)
	assert.Equal(t, ":8080", c.ListenAddr)
}

func TestLoad_DBPathOverride(t *testing.T) {
	t.Setenv("BUILDHOST_LISTEN_ADDR", "")
	t.Setenv("BUILDHOST_DATA_DIR", "")
	t.Setenv("BUILDHOST_DB_PATH", "/var/lib/buildhost.db")
	t.Setenv("BUILDHOST_BASE_URL", "")

	c := Load()
	assert.Equal(t, "/var/lib/buildhost.db", c.DBPath)
}

func TestLoad_BaseURLOverride(t *testing.T) {
	t.Setenv("BUILDHOST_LISTEN_ADDR", "")
	t.Setenv("BUILDHOST_DATA_DIR", "")
	t.Setenv("BUILDHOST_DB_PATH", "")
	t.Setenv("BUILDHOST_BASE_URL", "https://builds.example.com")

	c := Load()
	assert.Equal(t, "https://builds.example.com", c.BaseURL)
}

func TestLoad_AllOverrides(t *testing.T) {
	t.Setenv("BUILDHOST_LISTEN_ADDR", "0.0.0.0:443")
	t.Setenv("BUILDHOST_DATA_DIR", "/opt/data")
	t.Setenv("BUILDHOST_DB_PATH", "/opt/data/prod.db")
	t.Setenv("BUILDHOST_BASE_URL", "https://prod.example.com")

	c := Load()
	assert.Equal(t, "0.0.0.0:443", c.ListenAddr)
	assert.Equal(t, "/opt/data", c.DataDir)
	assert.Equal(t, "/opt/data/prod.db", c.DBPath)
	assert.Equal(t, "https://prod.example.com", c.BaseURL)
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
