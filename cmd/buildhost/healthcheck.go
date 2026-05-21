package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(healthcheckCmd)
}

var healthcheckCmd = &cobra.Command{
	Use:   "healthcheck",
	Short: "Check if the server is healthy",
	RunE: func(_ *cobra.Command, _ []string) error {
		addr := os.Getenv("BUILDHOST_LISTEN_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		if addr[0] == ':' {
			addr = "localhost" + addr
		}

		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get("http://" + addr + "/healthz")
		if err != nil {
			return fmt.Errorf("health check failed: %w", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
		}
		fmt.Println("ok")
		return nil
	},
}
