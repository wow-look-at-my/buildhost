package liveapply

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/config"
)

func TestGitHubFollowsTheSettings(t *testing.T) {
	t.Serial()
	t.Cleanup(func() { auth.SetGitHubToken("") })
	g := &GitHub{Ctx: context.Background()}

	require.NoError(t, g.Apply(config.Config{GitHubToken: "ghp_first"}))
	assert.Equal(t, "token", auth.GitHubCredentialSource(), "a saved token is in use at once")

	auth.SetGitHubToken("")
	require.NoError(t, g.Apply(config.Config{GitHubToken: "ghp_first", PrimaryDomain: "other.example"}))
	assert.Equal(t, "none", auth.GitHubCredentialSource(), "an unrelated change leaves the credential alone")

	require.NoError(t, g.Apply(config.Config{GitHubToken: "ghp_second"}))
	assert.Equal(t, "token", auth.GitHubCredentialSource())

	require.NoError(t, g.Apply(config.Config{GitHubToken: "ghp_second", SecretServerToken: "sst_unused"}))
	assert.Nil(t, g.stop, "a machine token starts no fetch loop while the settings hold a credential")

	require.NoError(t, g.Apply(config.Config{GitHubAppID: "12345"}))
	assert.Equal(t, "none", auth.GitHubCredentialSource(), "a cleared token stops at once, and half of an App is no credential")

	err := g.Apply(config.Config{SecretServerToken: "not-a-machine-token"})
	assert.ErrorContains(t, err, "machine token")
	assert.Nil(t, g.stop, "a refused machine token starts no fetch loop")
}

func TestTelemetryFollowsTheEndpoint(t *testing.T) {
	t.Serial()
	base := slog.Default()
	t.Cleanup(func() { slog.SetDefault(base) })
	tel := &Telemetry{}

	require.NoError(t, tel.Apply(""))
	assert.Nil(t, tel.stop, "no endpoint, no export")

	// Nothing listens here. The exporter connects when it sends, not at Init.
	require.NoError(t, tel.Apply("http://127.0.0.1:1"))
	require.NotNil(t, tel.stop)
	assert.NotSame(t, base, slog.Default(), "the log also goes to the endpoint")
	stop := tel.stop

	require.NoError(t, tel.Apply("http://127.0.0.1:1"))
	assert.NotNil(t, stop)
	assert.Equal(t, "http://127.0.0.1:1", tel.endpoint, "the same endpoint keeps its exporter")

	require.NoError(t, tel.Apply(""))
	assert.Nil(t, tel.stop, "an empty endpoint stops the export at once")
	assert.Same(t, base, slog.Default(), "the log goes back to stderr only")
}
