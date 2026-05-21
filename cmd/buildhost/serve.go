package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/buildhost/internal/admin"
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
		if err := os.MkdirAll(cfg.DataDir+"/tmp", 0o755); err != nil {
			return fmt.Errorf("create temp dir: %w", err)
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

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()

		if cfg.AdminListenAddr != "" {
			adminSrv := admin.New(cfg, database, admin.BuildInfo{
				Version: buildVersion,
				Commit:  buildCommit,
				Date:    buildDate,
				RepoURL: "https://github.com/wow-look-at-my/buildhost",
			})
			go func() {
				slog.Info("starting admin dashboard", "addr", cfg.AdminListenAddr)
				if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					slog.Error("admin server error", "err", err)
					os.Exit(1)
				}
			}()
		}

		srv := server.New(cfg, database, store)
		slog.Info("starting server", "addr", cfg.ListenAddr, "base_url", cfg.BaseURL)

		go func() {
			<-ctx.Done()
			slog.Info("shutting down")
			srv.Shutdown(context.Background())
		}()

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	},
}
