package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	tryUpdateCmd.Flags().String("addr", "", "server address (default: BUILDHOST_LISTEN_ADDR or :8080)")
	rootCmd.AddCommand(tryUpdateCmd)
}

var tryUpdateCmd = &cobra.Command{
	Use:   "try-update",
	Short: "Exit 0 if server is idle, non-zero otherwise. Intended for docker-updater pre-update checks.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		addr, _ := cmd.Flags().GetString("addr")
		if addr == "" {
			addr = os.Getenv("BUILDHOST_LISTEN_ADDR")
			if addr == "" {
				addr = ":8080"
			}
		}
		if addr[0] == ':' {
			addr = "127.0.0.1" + addr
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/ready-to-update", nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			fmt.Println("idle")
			return nil
		}

		fmt.Fprintf(os.Stderr, "server not ready (status %d)\n", resp.StatusCode)
		os.Exit(1)
		return nil
	},
}
