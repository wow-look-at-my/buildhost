package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/uploadclient"
)

// addChunkSizeFlag registers --chunk-size on an upload-capable command.
// Files larger than the server's direct-upload limit (from
// GET /api/v1/server-info) are uploaded through a chunked upload session in
func addChunkSizeFlag(cmd *cobra.Command) {
	cmd.Flags().String("chunk-size", "",
		fmt.Sprintf("Chunk size for large uploads (e.g. 64M); 0 disables chunking (default %dM)",
			uploadclient.DefaultChunkSize>>20))
}

// newUploader builds an uploader from the command's shared flags.
func newUploader(cmd *cobra.Command, server, token string) (*uploadclient.Uploader, error) {
	u := &uploadclient.Uploader{
		Server: server,
		Token:  token,
		Stdout: cmd.OutOrStdout(),
	}
	if raw, _ := cmd.Flags().GetString("chunk-size"); raw != "" {
		n, err := config.ParseByteSize(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid --chunk-size: %w", err)
		}
		if n == 0 {
			n = -1
		}
		u.ChunkSize = n
	}
	return u, nil
}
