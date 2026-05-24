package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

		store, err := storage.NewFilesystem(cfg.DataDir+"/blobs", cfg.StorageCompress)
		if err != nil {
			return fmt.Errorf("init storage: %w", err)
		}

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()

		errc := make(chan error, 2)

		var adminHTTP *http.Server
		if cfg.AdminListenAddr != "" {
			adminDash := admin.New(cfg, database, admin.BuildInfo{
				Version: buildVersion,
				Commit:  buildCommit,
				Date:    buildDate,
				RepoURL: "https://github.com/wow-look-at-my/buildhost",
			})
			adminHTTP = adminDash.NewHTTPServer()
			go func() {
				slog.Info("starting admin dashboard", "addr", cfg.AdminListenAddr)
				if err := adminHTTP.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					errc <- fmt.Errorf("admin server: %w", err)
				}
			}()
		}

		srv := server.New(cfg, database, store)
		slog.Info("starting server", "addr", cfg.ListenAddr, "base_url", cfg.BaseURL)

		go func() {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				errc <- fmt.Errorf("main server: %w", err)
			}
		}()

		select {
		case err := <-errc:
			return err
		case <-ctx.Done():
		}

		slog.Info("shutting down, waiting for in-flight requests")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer shutdownCancel()

		var shutdownErr error
		if err := srv.Shutdown(shutdownCtx); err != nil {
			shutdownErr = fmt.Errorf("main server shutdown: %w", err)
		}
		if adminHTTP != nil {
			if err := adminHTTP.Shutdown(shutdownCtx); err != nil {
				shutdownErr = fmt.Errorf("admin server shutdown: %w", err)
			}
		}

		if shutdownErr != nil {
			if shutdownCtx.Err() == context.DeadlineExceeded {
				slog.Error("shutdown timed out after 5 minutes")
			}
			return shutdownErr
		}
		slog.Info("shutdown complete")
		return nil
	},
}
