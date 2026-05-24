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

	"github.com/cloudflare/tableflip"
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/buildhost/internal/admin"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/server"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/buildhost/internal/telemetry"
)

func init() {
	rootCmd.AddCommand(serveCmd)
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the registry server",
	RunE: func(_ *cobra.Command, _ []string) error {
		cfg := config.Load()

		if cfg.OTELEndpoint != "" {
			shutdown, err := telemetry.Init(context.Background(), cfg.OTELEndpoint, resolvedVersion())
			if err != nil {
				return fmt.Errorf("init telemetry: %w", err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				shutdown(ctx)
			}()
			slog.Info("telemetry enabled", "endpoint", cfg.OTELEndpoint)
		}

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

		fsStore, err := storage.NewFilesystem(cfg.DataDir+"/blobs", cfg.StorageCompress)
		if err != nil {
			return fmt.Errorf("init storage: %w", err)
		}
		store := storage.NewTraced(fsStore)

		upg, err := tableflip.New(tableflip.Options{
			UpgradeTimeout: 30 * time.Second,
			PIDFile:        cfg.DataDir + "/buildhost.pid",
		})
		if err != nil {
			return fmt.Errorf("init tableflip: %w", err)
		}
		defer upg.Stop()

		go func() {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGHUP)
			for range sig {
				if err := upg.Upgrade(); err != nil {
					slog.Error("upgrade failed", "err", err)
				}
			}
		}()

		mainLn, err := upg.Listen("tcp", cfg.ListenAddr)
		if err != nil {
			return fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
		}

		errc := make(chan error, 2)

		var adminHTTP *http.Server
		if cfg.AdminListenAddr != "" {
			adminLn, err := upg.Listen("tcp", cfg.AdminListenAddr)
			if err != nil {
				return fmt.Errorf("listen admin %s: %w", cfg.AdminListenAddr, err)
			}

			adminDash := admin.New(cfg, database, admin.BuildInfo{
				Version: resolvedVersion(),
				Commit:  resolvedCommit(),
				Date:    resolvedDate(),
				RepoURL: "https://github.com/wow-look-at-my/buildhost",
			})
			adminHTTP = adminDash.NewHTTPServer()
			go func() {
				slog.Info("starting admin dashboard", "addr", cfg.AdminListenAddr)
				if err := adminHTTP.Serve(adminLn); err != nil && err != http.ErrServerClosed {
					errc <- fmt.Errorf("admin server: %w", err)
				}
			}()
		}

		srv := server.New(cfg, database, store)
		slog.Info("starting server", "addr", cfg.ListenAddr, "base_url", cfg.BaseURL, "pid", os.Getpid())

		go func() {
			if err := srv.Serve(mainLn); err != nil && err != http.ErrServerClosed {
				errc <- fmt.Errorf("main server: %w", err)
			}
		}()

		if err := upg.Ready(); err != nil {
			return fmt.Errorf("tableflip ready: %w", err)
		}

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()

		select {
		case err := <-errc:
			return err
		case <-upg.Exit():
			slog.Info("graceful upgrade: new process ready, draining connections")
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
