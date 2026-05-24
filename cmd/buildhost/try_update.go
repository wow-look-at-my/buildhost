package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	tryUpdateCmd.Flags().String("admin", "http://127.0.0.1:9090", "admin server address")
	rootCmd.AddCommand(tryUpdateCmd)
}

var tryUpdateCmd = &cobra.Command{
	Use:   "try-update",
	Short: "Exit 0 if server is idle, non-zero otherwise. Intended for watchtower pre-update lifecycle hooks.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		addr, _ := cmd.Flags().GetString("admin")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/admin/inflight", nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "admin returned status %d\n", resp.StatusCode)
			os.Exit(1)
		}

		var result struct {
			Inflight int64 `json:"inflight"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		if result.Inflight > 0 {
			fmt.Fprintf(os.Stderr, "buildhost busy: %d in-flight write(s), skipping update\n", result.Inflight)
			os.Exit(1)
		}

		fmt.Println("idle")
		return nil
	},
}
