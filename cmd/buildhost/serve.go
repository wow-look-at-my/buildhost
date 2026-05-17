package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/server"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

func init() {
	rootCmd.AddCommand(serveCmd)
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the registry server",
	RunE: func(_ *cobra.Command, _ []string) error {
		cfg := config.Load()

		if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
			return fmt.Errorf("create data dir: %w", err)
		}

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer database.Close()

		store, err := storage.NewFilesystem(cfg.DataDir + "/blobs")
		if err != nil {
			return fmt.Errorf("init storage: %w", err)
		}

		srv := server.New(cfg, database, store)
		slog.Info("starting server", "addr", cfg.ListenAddr, "base_url", cfg.BaseURL)
		return srv.ListenAndServe()
	},
}
