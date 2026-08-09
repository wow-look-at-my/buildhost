package ociclient

// Authenticating docker to a buildhost registry. This lives here, in the
// buildhost repo, because it is buildhost's business: a consumer that has to
// know the audience, the token endpoint and the registry hostname is a consumer
// reimplementing this file, and it will drift.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// RegistryFor returns the OCI registry host for a buildhost server URL. The
// registry is a subdomain of the server, and the server URL itself is the OIDC
// audience, so the two are never interchangeable.
func RegistryFor(server string) (string, error) {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("not a server URL: %q", server)
	}
	return "oci." + u.Host, nil
}

// ActionsOIDCToken mints a GitHub Actions OIDC token with this server as its
// audience. buildhost verifies the JWT directly, so no static credential is
// stored anywhere; the token is short-lived, which is why it is minted
// immediately before the operation that needs it rather than once per job.
func ActionsOIDCToken(ctx context.Context, server string) (string, error) {
	endpoint := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL")
	requestToken := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if endpoint == "" || requestToken == "" {
		return "", fmt.Errorf("no OIDC token available: add 'permissions: { id-token: write }' to this job")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"&audience="+url.QueryEscape(server), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+requestToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request OIDC token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("request OIDC token: %s: %s", resp.Status, readErrBody(resp))
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode OIDC token: %w", err)
	}
	if body.Value == "" {
		return "", fmt.Errorf("the OIDC token endpoint returned no token")
	}
	return body.Value, nil
}

// DockerLogin authenticates the local docker CLI to registry. The token goes in
// on stdin so it never reaches a process listing, and it is masked first so a
// workflow log cannot echo it back out of a later docker error.
func DockerLogin(ctx context.Context, registry, token string) error {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		fmt.Printf("::add-mask::%s\n", token)
	}
	cmd := exec.CommandContext(ctx, "docker", "login", registry, "-u", "oidc", "--password-stdin")
	cmd.Stdin = strings.NewReader(token)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker login %s: %w", registry, err)
	}
	return nil
}
