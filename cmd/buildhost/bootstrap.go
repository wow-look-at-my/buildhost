package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func init() {
	rootCmd.AddCommand(bootstrapCmd)
}

var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Create the initial admin token",
	Long:  "Creates the first admin token for the server. Only works when no tokens exist yet.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg := config.Load()

		if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
			return fmt.Errorf("create data dir: %w", err)
		}

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer database.Close()

		tokens, err := database.ListTokens(cmd.Context())
		if err != nil {
			return fmt.Errorf("check existing tokens: %w", err)
		}
		if len(tokens) > 0 {
			return fmt.Errorf("tokens already exist (%d found) — bootstrap is only for first-time setup", len(tokens))
		}

		name, _ := cmd.Flags().GetString("name")
		plaintext, token, err := database.CreateToken(cmd.Context(), name, nil, "read,write")
		if err != nil {
			return fmt.Errorf("create token: %w", err)
		}

		fmt.Fprintf(os.Stderr, "Admin token created (id=%d, name=%q)\n", token.ID, token.Name)
		fmt.Println(plaintext)
		return nil
	},
}

func init() {
	bootstrapCmd.Flags().String("name", "admin", "Name for the bootstrap token")
}
