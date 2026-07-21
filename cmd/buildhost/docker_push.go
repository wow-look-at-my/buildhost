package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/ociclient"
)

func init() {
	rootCmd.AddCommand(dockerPushCmd)
}

var dockerPushCmd = &cobra.Command{
	Use:   "docker-push --image <oci-layout> <registry>/<project>[:tag] ...",
	Short: "Push a locally built container image to a buildhost OCI registry",
	Long: `Push a locally built container image to a buildhost OCI registry.

The image is read from an OCI image layout -- a directory or tarball produced
by "docker buildx build --output type=oci" or "docker save". Unlike
docker/buildx/crane (which send every layer as one request), blobs larger than
the server's advertised safe request size are uploaded in sequential chunks,
so layers of any size pass proxies that cap request bodies (Cloudflare's edge
rejects single requests over ~100 MB).

Every reference must name the same registry and project; each adds one tag.`,
	Example: `  buildhost docker-push --token $TOKEN --image ./image.oci.tar \
      oci.builds.example.com/myproject:v1.2.3 oci.builds.example.com/myproject:latest`,
	Args: cobra.MinimumNArgs(1),
	RunE: runDockerPush,
}

func init() {
	dockerPushCmd.Flags().String("image", "", "Path to the OCI image layout (directory or tarball) to push")
	dockerPushCmd.Flags().String("token", "", "API token or OIDC JWT (defaults to $BUILDHOST_TOKEN)")
	dockerPushCmd.Flags().String("server", "", "Apex server URL for /api/v1/server-info (default: derived from the registry host)")
	dockerPushCmd.Flags().Bool("plain-http", false, "Use http:// instead of https:// (local/test servers)")
	addChunkSizeFlag(dockerPushCmd)
}

func runDockerPush(cmd *cobra.Command, refs []string) error {
	imagePath, _ := cmd.Flags().GetString("image")
	token, _ := cmd.Flags().GetString("token")
	server, _ := cmd.Flags().GetString("server")
	plainHTTP, _ := cmd.Flags().GetBool("plain-http")

	if imagePath == "" {
		return fmt.Errorf("--image is required")
	}
	if token == "" {
		token = os.Getenv("BUILDHOST_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("--token (or BUILDHOST_TOKEN) is required")
	}

	registry, project, tags, err := ociclient.ParseRefs(refs)
	if err != nil {
		return err
	}

	p := &ociclient.Pusher{
		Registry:  registry,
		Project:   project,
		Token:     token,
		Server:    server,
		PlainHTTP: plainHTTP,
		Stdout:    cmd.OutOrStdout(),
	}
	if raw, _ := cmd.Flags().GetString("chunk-size"); raw != "" {
		n, err := config.ParseByteSize(raw)
		if err != nil {
			return fmt.Errorf("invalid --chunk-size: %w", err)
		}
		if n == 0 {
			n = -1 // 0 means "disable chunking" (always one request per blob)
		}
		p.ChunkSize = n
	}

	if err := p.Push(imagePath, tags); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "pushed %s/%s (%d tag(s))\n", registry, project, len(tags))
	return nil
}
