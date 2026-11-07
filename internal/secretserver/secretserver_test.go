package secretserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

func fakeServer(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sst_good" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid token"}`))
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { auth.SetGitHubToken("") })
	return srv.URL
}

func TestSource_AppliesTheVendedToken(t *testing.T) {
	t.Serial()
	url := fakeServer(t, 200, `{"BUILDHOST_GITHUB_TOKEN":"ghp_x","OTHER":"y"}`)
	s := &Source{URL: url, Token: "sst_good"}
	assert.Equal(t, RefreshInterval, s.refresh(t.Context()))
	assert.Equal(t, "token", auth.GitHubCredentialSource())
	st := LastStatus()
	assert.True(t, st.OK)
	assert.Equal(t, "token", st.Applied)
	assert.NotContains(t, st.Error, "ghp_x")
}

func TestSource_UnentitledTokenFailsLoud(t *testing.T) {
	t.Serial()
	url := fakeServer(t, 200, `{}`)
	s := &Source{URL: url, Token: "sst_good"}
	assert.Equal(t, RetryInterval, s.refresh(t.Context()), "a failure retries soon")
	st := LastStatus()
	assert.False(t, st.OK)
	assert.Contains(t, st.Error, SecretGitHubToken)
	assert.Equal(t, "none", auth.GitHubCredentialSource())
}

func TestSource_RejectedTokenKeepsTheCredentialInUse(t *testing.T) {
	t.Serial()
	url := fakeServer(t, 200, `{"BUILDHOST_GITHUB_TOKEN":"ghp_x"}`)
	s := &Source{URL: url, Token: "sst_good"}
	s.refresh(t.Context())
	s.Token = "sst_revoked"
	assert.Equal(t, RetryInterval, s.refresh(t.Context()))
	assert.Contains(t, LastStatus().Error, "401")
	assert.Equal(t, "token", auth.GitHubCredentialSource(), "a failed fetch keeps the token already in use")
}

func TestStart_RefusesANonMachineToken(t *testing.T) {
	t.Serial()
	err := (&Source{URL: "http://127.0.0.1:1", Token: "ghp_wrong"}).Start(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), MachineTokenPrefix)
}
