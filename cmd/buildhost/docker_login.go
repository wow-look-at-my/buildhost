package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/buildhost/internal/ociclient"
)

func init() {
	rootCmd.AddCommand(dockerLoginCmd)
	dockerLoginCmd.Flags().String("server", "", "Buildhost server URL (defaults to $BUILDHOST_SERVER)")
	dockerLoginCmd.Flags().String("token", "", "API token (defaults to $BUILDHOST_TOKEN; without one, a GitHub Actions OIDC token is minted)")
}

var dockerLoginCmd = &cobra.Command{
	Use:   "docker-login --server <url>",
	Short: "Authenticate the local docker CLI to a buildhost OCI registry",
	Long: `Authenticate the local docker CLI to a buildhost OCI registry.

Without --token this mints a GitHub Actions OIDC token for the server, which is
what a workflow wants: nothing static is stored, and buildhost verifies the JWT
itself. The job needs "permissions: { id-token: write }".

Run it immediately before the docker operation that needs it. The credential is
deliberately short-lived, so logging in once at the top of a long job leaves the
pull at the bottom unauthenticated.`,
	Example: `  buildhost docker-login --server https://builds.example.com`,
	Args:    cobra.NoArgs,
	RunE:    runDockerLogin,
}

func runDockerLogin(cmd *cobra.Command, _ []string) error {
	server, _ := cmd.Flags().GetString("server")
	if server == "" {
		server = os.Getenv("BUILDHOST_SERVER")
	}
	if server == "" {
		return fmt.Errorf("--server (or BUILDHOST_SERVER) is required")
	}
	registry, err := ociclient.RegistryFor(server)
	if err != nil {
		return err
	}

	token, _ := cmd.Flags().GetString("token")
	if token == "" {
		token = os.Getenv("BUILDHOST_TOKEN")
	}
	if token == "" {
		if token, err = ociclient.ActionsOIDCToken(cmd.Context(), server); err != nil {
			return err
		}
	}

	if err := ociclient.DockerLogin(cmd.Context(), registry, token); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "logged in to %s\n", registry)
	return nil
}
