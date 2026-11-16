package config

import (
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoredValueTakesPrecedence(t *testing.T) {
	t.Setenv("BUILDHOST_PRIMARY_DOMAIN", "env.example")
	t.Setenv("BUILDHOST_CACHE_KEEP", "1h")
	t.Cleanup(func() { SetStored(nil) })

	SetStored(map[string]string{"BUILDHOST_PRIMARY_DOMAIN": "Stored.Example", "BUILDHOST_MAX_UPLOAD_SIZE": "3G"})
	c := Load()
	assert.Equal(t, "stored.example", c.PrimaryDomain)
	assert.Equal(t, "dashboard", Source("BUILDHOST_PRIMARY_DOMAIN"))
	assert.Equal(t, "environment", Source("BUILDHOST_CACHE_KEEP"))
	assert.Equal(t, "default", Source("BUILDHOST_MIRROR_KEEP"))
	assert.Equal(t, int64(3<<30), MaxUploadSize(), "an upload limit reads the stored value on each use")
}

func TestReloadGivesTheNewConfigToEachSubscriber(t *testing.T) {
	t.Setenv("BUILDHOST_PRIMARY_DOMAIN", "env.example")
	t.Cleanup(func() {
		SetStored(nil)
		Reload()
	})
	var got []Config
	OnChange(func(c Config) { got = append(got, c) })

	SetStored(map[string]string{"BUILDHOST_PRIMARY_DOMAIN": "one.example", "BUILDHOST_CACHE_KEEP": "2h"})
	c := Reload()
	require.Len(t, got, 1)
	assert.Equal(t, "one.example", got[0].PrimaryDomain)
	assert.Equal(t, c, Current(), "Current is the configuration the last Reload loaded")
	assert.Equal(t, 2*time.Hour, Current().CacheKeep)

	SetStored(map[string]string{"BUILDHOST_PRIMARY_DOMAIN": "two.example"})
	assert.Equal(t, "one.example", Current().PrimaryDomain, "a stored value is not in use until Reload")
	Reload()
	require.Len(t, got, 2)
	assert.Equal(t, "two.example", Current().PrimaryDomain)
}

func TestCheckChange(t *testing.T) {
	t.Setenv("BUILDHOST_PRIMARY_DOMAIN", "")
	t.Setenv("BUILDHOST_GITHUB_APP_ID", "")
	t.Setenv("BUILDHOST_GITHUB_TOKEN", "")
	t.Cleanup(func() { SetStored(nil) })
	SetStored(map[string]string{"BUILDHOST_PRIMARY_DOMAIN": "example.com"})
	str := func(s string) *string { return &s }

	for _, tc := range []struct {
		name  string
		value *string
		want  string
	}{
		{"BUILDHOST_RUN_FILES_KEEP", str("soon"), "duration"},
		{"BUILDHOST_MAX_UPLOAD_SIZE", str("big"), "positive size"},
		{"BUILDHOST_CACHE_MIN_FREE_PERCENT", str("100"), "from 0 up to 100"},
		{"BUILDHOST_RETENTION_KEEP_N", str("-1"), "non-negative integer"},
		{"BUILDHOST_RETENTION_ENFORCE", str("yes"), "true or false"},
		{"BUILDHOST_PRIMARY_DOMAIN", str("https://example.com/"), "not a domain name"},
		{"BUILDHOST_PRIMARY_DOMAIN", str("example.com:8080"), "not a domain name"},
		{"BUILDHOST_SITE_FETCH_DOMAINS", str("ok.example, bad domain"), "not a domain name"},
		{"BUILDHOST_OIDC_ISSUERS", str("https://token.actions.githubusercontent.com,token.example"), "not a URL"},
		{"BUILDHOST_OTEL_ENDPOINT", str("collector:4318"), "not a URL"},
		{"BUILDHOST_GOPROXY_UPSTREAM", str("proxy.golang.org"), "not a URL"},
		{"BUILDHOST_GOPROXY_READINESS_MODULE", str("https://github.com/org/repo"), "not a module path"},
		{"BUILDHOST_SECRET_SERVER_TOKEN", str("ghp_x"), "sst_"},
		{"BUILDHOST_DATA_DIR", str("/tmp"), "environment only"},
		{"BUILDHOST_PRIMARY_DOMAIN", str(""), "BUILDHOST_PRIMARY_DOMAIN is required"},
		{"BUILDHOST_PRIMARY_DOMAIN", nil, "BUILDHOST_PRIMARY_DOMAIN is required"},
		{"BUILDHOST_NOPE", str("x"), "not a setting"},
	} {
		_, err := CheckChange(tc.name, tc.value)
		if assert.Error(t, err, "%s", tc.name) {
			assert.Contains(t, err.Error(), tc.want, tc.name)
		}
	}

	for _, tc := range []struct{ name, value string }{
		{"BUILDHOST_PRIMARY_DOMAIN", "*"},
		{"BUILDHOST_PRIMARY_DOMAIN", "Other.Example"},
		{"BUILDHOST_SITE_DOMAIN", "*.sites.example"},
		{"BUILDHOST_SITE_FETCH_DOMAINS", "a.example, 127.0.0.1"},
		{"BUILDHOST_OIDC_ISSUERS", "https://token.actions.githubusercontent.com"},
		{"BUILDHOST_OTEL_ENDPOINT", "http://collector:4318"},
		{"BUILDHOST_GOPROXY_PRIVATE_PREFIXES", "github.com/org, example.com/team"},
		{"BUILDHOST_RUN_FILES_KEEP", "6h"},
		{"BUILDHOST_OTEL_ENDPOINT", ""},
	} {
		warning, err := CheckChange(tc.name, &tc.value)
		assert.NoError(t, err, "%s=%s", tc.name, tc.value)
		assert.Empty(t, warning, "%s=%s", tc.name, tc.value)
	}

	warning, err := CheckChange("BUILDHOST_GITHUB_APP_ID", str("12345"))
	require.NoError(t, err, "half of the App pair saves, so the operator can enter the other half")
	assert.Contains(t, warning, "BUILDHOST_GITHUB_APP_PRIVATE_KEY is empty")

	SetStored(map[string]string{"BUILDHOST_PRIMARY_DOMAIN": "example.com", "BUILDHOST_GITHUB_TOKEN": "ghp_x"})
	warning, err = CheckChange("BUILDHOST_SECRET_SERVER_TOKEN", str("sst_abc"))
	require.NoError(t, err)
	assert.Contains(t, warning, "is not used while")

	assert.Equal(t, "example.com", Getenv("BUILDHOST_PRIMARY_DOMAIN"), "a check changes nothing")
	_, ok := StoredValue("BUILDHOST_SECRET_SERVER_TOKEN")
	assert.False(t, ok, "a check stores nothing")
}

// Every BUILDHOST_* variable the server reads must be on the Settings page.
func TestSettingsListEveryVariable(t *testing.T) {
	re := regexp.MustCompile(`"(BUILDHOST_[A-Z0-9_]+)"`)
	for _, f := range []string{"config.go", "../goproxy/goproxy.go"} {
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			_, ok := LookupSetting(m[1])
			assert.True(t, ok, "%s reads %s, which config.Settings does not list", f, m[1])
		}
	}
}
