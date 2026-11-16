package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/config"
)

func TestLiveGitHubFollowsTheSettings(t *testing.T) {
	t.Cleanup(func() { auth.SetGitHubToken("") })
	g := &liveGitHub{ctx: context.Background()}

	require.NoError(t, g.apply(config.Config{GitHubToken: "ghp_first"}))
	assert.Equal(t, "token", auth.GitHubCredentialSource(), "a saved token is in use at once")

	// The same settings leave the credential alone.
	auth.SetGitHubToken("")
	require.NoError(t, g.apply(config.Config{GitHubToken: "ghp_first"}))
	assert.Equal(t, "none", auth.GitHubCredentialSource())

	require.NoError(t, g.apply(config.Config{GitHubToken: "ghp_second"}))
	assert.Equal(t, "token", auth.GitHubCredentialSource())

	require.NoError(t, g.apply(config.Config{}))
	assert.Equal(t, "none", auth.GitHubCredentialSource(), "a cleared token stops at once")

	err := g.apply(config.Config{SecretServerToken: "not-a-machine-token"})
	assert.ErrorContains(t, err, "machine token")
	assert.Nil(t, g.stop, "a refused machine token starts no fetch loop")
}

func TestLiveTelemetryWithNoEndpointDoesNothing(t *testing.T) {
	tel := &liveTelemetry{}
	require.NoError(t, tel.apply(""))
	assert.Nil(t, tel.stop)
	tel.shutdown()
}
