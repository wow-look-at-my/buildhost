package ociclient

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryFor(t *testing.T) {
	for _, tc := range []struct{ server, want string }{
		{"https://pazer.build", "oci.pazer.build"},
		{"https://builds.example.com:8443", "oci.builds.example.com:8443"},
		{"http://127.0.0.1:8088", "oci.127.0.0.1:8088"},
	} {
		got, err := RegistryFor(tc.server)
		require.NoError(t, err, tc.server)
		assert.Equal(t, tc.want, got)
	}

	_, err := RegistryFor("pazer.build")
	assert.Error(t, err, "a bare host is not a server URL -- the scheme is what makes it the OIDC audience")
}

func TestActionsOIDCToken(t *testing.T) {
	var gotAudience, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAudience = r.URL.Query().Get("audience")
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"value":"the.jwt.here"}`))
	}))
	defer srv.Close()

	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", srv.URL+"/?api-version=2.0")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "runner-secret")

	token, err := ActionsOIDCToken(t.Context(), "https://pazer.build")
	require.NoError(t, err)
	assert.Equal(t, "the.jwt.here", token)
	assert.Equal(t, "https://pazer.build", gotAudience, "the audience is the server URL buildhost verifies against")
	assert.Equal(t, "Bearer runner-secret", gotAuth)
}

// A job without id-token permission must be told exactly that, rather than
// failing later as an unexplained registry 401.
func TestActionsOIDCToken_WithoutPermissionSaysSo(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")

	_, err := ActionsOIDCToken(t.Context(), "https://pazer.build")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "id-token: write")
}

func TestActionsOIDCToken_EmptyTokenIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", srv.URL+"/?api-version=2.0")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "runner-secret")

	_, err := ActionsOIDCToken(t.Context(), "https://pazer.build")
	assert.Error(t, err, "an empty token must never be handed to docker login as if it were one")
}
