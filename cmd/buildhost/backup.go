package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func init() {
	rootCmd.AddCommand(backupCmd)
}

var backupCmd = &cobra.Command{
	Use:   "backup [output]",
	Short: "Write a consistent copy of the database",
	Long: "Copies the SQLite database (BUILDHOST_DB_PATH) to output with VACUUM INTO. " +
		"Safe against a running server, and never modifies the source. Without output, " +
		"writes buildhost-backup-<UTC timestamp>.db beside the database. Refuses to " +
		"overwrite an existing file. Blobs under BUILDHOST_DATA_DIR are not included.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()

		out := filepath.Join(filepath.Dir(cfg.DBPath),
			fmt.Sprintf("buildhost-backup-%s.db", time.Now().UTC().Format("20060102T150405Z")))
		if len(args) == 1 {
			out = args[0]
		}

		if err := db.Backup(cmd.Context(), cfg.DBPath, out); err != nil {
			return err
		}
		info, err := os.Stat(out)
		if err != nil {
			return fmt.Errorf("stat backup: %w", err)
		}
		fmt.Printf("backed up %s to %s (%s)\n", cfg.DBPath, out, humanBytes(info.Size()))
		return nil
	},
}
