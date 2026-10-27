package config

import (
	"os"
	"regexp"
	"testing"

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
	assert.Equal(t, int64(3<<30), MaxUploadSize(), "a Live setting reads the stored value on each use")

	MarkStarted()
	SetStored(map[string]string{"BUILDHOST_PRIMARY_DOMAIN": "other.example", "BUILDHOST_MAX_UPLOAD_SIZE": "4G"})
	primary, _ := LookupSetting("BUILDHOST_PRIMARY_DOMAIN")
	upload, _ := LookupSetting("BUILDHOST_MAX_UPLOAD_SIZE")
	assert.True(t, RestartPending(primary))
	assert.False(t, RestartPending(upload), "a Live setting never waits for a restart")
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
