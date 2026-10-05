package auth

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A refused cross-repo read says which credential source buildhost lacks. The
// bare "no credential" text reads the same for an unconfigured deployment and
// for a mint GitHub refused, and only one of those is fixed by configuration.
func TestNoRepoCredentialReasonNamesTheMissingSource(t *testing.T) {
	t.Serial()
	require.NoError(t, SetGitHubApp("", ""))
	SetGitHubToken("")
	t.Cleanup(func() { SetGitHubToken("") })
	assert.Equal(t, "no GitHub App and no BUILDHOST_GITHUB_TOKEN are configured", noRepoCredentialReason())

	SetGitHubToken("ghp_x")
	assert.Contains(t, noRepoCredentialReason(), "BUILDHOST_GITHUB_TOKEN is set")

	withStubGitHubApp(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	assert.Contains(t, noRepoCredentialReason(), "could not mint an installation token")

	_, _, reason := fetchUserRepoPermission(t.Context(), "PazerOP", "wow-look-at-my/gh-wait-ci")
	assert.Contains(t, reason, "could not mint an installation token")
}
