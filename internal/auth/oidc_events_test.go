package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- OIDC event-allowlist tests ---
//
// Auto-provisioning gates on the token's event_name claim against the configured
// allowlist (BUILDHOST_OIDC_EVENTS, defaulting to push,pull_request,workflow_dispatch
// in config.Load). Every default event implies the actor has write access to the
// repo, so a trusted-issuer token of that event can create/publish projects.

func TestVerifyToken_TrustedIssuer_RejectedEvent(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-event-bad")

	claims := map[string]any{
		"iss":        srv.URL,
		"sub":        "repo:myorg/myrepo:ref:refs/heads/main",
		"exp":        time.Now().Add(10 * time.Minute).Unix(),
		"iat":        time.Now().Unix(),
		"event_name": "pull_request",
	}
	token := signJWT(t, key, "kid-event-bad", claims)

	v := NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{srv.URL}, AllowedOrgs: []string{"*"}, AllowedEvents: []string{"push"}})
	_, _, err = v.VerifyToken(context.Background(), token, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "event")
}

// A workflow_dispatch-triggered run (a manual dispatch) carries
// event_name == "workflow_dispatch". It is in the default allowed-events set
// (push, pull_request, workflow_dispatch -- see config.Load) because GitHub only
// lets users with write access to a repo trigger a manual run, and fork actors
// never receive an OIDC token, so it carries the same write-access guarantee as
// push. Auto-provisioning must accept it out of the box, otherwise a manual
// release/publish dispatch 401s at docker login.
func TestVerifyToken_TrustedIssuer_WorkflowDispatchAccepted(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-event-dispatch")

	claims := map[string]any{
		"iss":        srv.URL,
		"sub":        "repo:PazerOP/UE553:ref:refs/heads/main",
		"exp":        time.Now().Add(10 * time.Minute).Unix(),
		"iat":        time.Now().Unix(),
		"event_name": "workflow_dispatch",
	}
	token := signJWT(t, key, "kid-event-dispatch", claims)

	// Mirror the production default allowlist from config.Load's
	// len(OIDCEvents)==0 branch: push, pull_request, workflow_dispatch.
	v := NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{srv.URL}, AllowedOrgs: []string{"*"}, AllowedEvents: []string{"push", "pull_request", "workflow_dispatch"}})
	_, oidcProject, err := v.VerifyToken(context.Background(), token, nil)
	require.NoError(t, err)
	assert.Equal(t, "ue553", oidcProject)
}
