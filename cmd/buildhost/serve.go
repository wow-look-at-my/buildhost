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
	"github.com/wow-look-at-my/buildhost/internal/buildinfo"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/retention"
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
			shutdown, err := telemetry.Init(context.Background(), cfg.OTELEndpoint, buildinfo.Version())
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

		// Seed the UI-editable retention policy from env defaults on first start
		// (INSERT OR IGNORE -- never clobbers later dashboard edits).
		if err := database.SeedRetentionSettings(context.Background(), cfg.RetentionKeepN, int(cfg.RetentionRecencyGuard.Hours())); err != nil {
			return fmt.Errorf("seed retention settings: %w", err)
		}

		fsStore, err := storage.NewFilesystem(cfg.DataDir+"/blobs", cfg.StorageCompress)
		if err != nil {
			return fmt.Errorf("init storage: %w", err)
		}
		store := storage.NewTraced(fsStore)

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()

		errc := make(chan error, 2)

		var adminHTTP *http.Server
		if cfg.AdminListenAddr != "" {
			adminDash := admin.New(cfg, database, store, admin.BuildInfo{
				Version: buildinfo.Version(),
				Commit:  buildinfo.Commit(),
				Date:    buildinfo.Date(),
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
		slog.Info("starting server", "addr", cfg.ListenAddr)

		go func() {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				errc <- fmt.Errorf("main server: %w", err)
			}
		}()

		startRetentionSweeper(ctx, cfg, database, store)

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

// startRetentionSweeper launches the background GC sweeper when a positive
// interval is configured. It is report-only unless BUILDHOST_RETENTION_ENFORCE is
// set, defers a tick while writes are in flight (the same counter /ready-to-update
// uses), and exits when ctx is cancelled at shutdown.
func startRetentionSweeper(ctx context.Context, cfg config.Config, database *db.DB, store storage.Storage) {
	if cfg.RetentionInterval <= 0 {
		return
	}
	slog.Info("retention sweeper enabled",
		"interval", cfg.RetentionInterval, "enforce", cfg.RetentionEnforce)
	go func() {
		t := time.NewTicker(cfg.RetentionInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n := admin.InflightWrites(); n > 0 {
					slog.Info("retention: deferring sweep, writes in flight", "inflight", n)
					continue
				}
				// Read the live (dashboard-editable) policy each cycle so edits
				// apply without a restart. enforce stays env-gated.
				settings, err := database.GetRetentionSettings(ctx)
				if err != nil {
					slog.Error("retention sweep: load settings failed", "err", err)
					continue
				}
				ret := retention.New(database, store, retention.ConfigFromSettings(settings, cfg.RetentionEnforce))
				rep, err := ret.Run(ctx)
				if err != nil {
					slog.Error("retention sweep failed", "err", err)
					continue
				}
				logRetentionReport(rep)
			}
		}
	}()
}

func logRetentionReport(rep retention.Report) {
	if rep.Enforced {
		for _, r := range rep.EvictedReleases {
			slog.Warn("retention evicted release", "reason", "keep-n",
				"project_id", r.ProjectID, "branch", r.Branch, "version", r.Version, "release_id", r.ID)
		}
		for _, r := range rep.AbandonedReleases {
			slog.Warn("retention evicted release", "reason", "abandoned",
				"project_id", r.ProjectID, "branch", r.Branch, "version", r.Version, "release_id", r.ID)
		}
	}
	if rep.Releases() == 0 {
		return // nothing to report this cycle
	}
	slog.Info("retention sweep complete",
		"enforced", rep.Enforced, "releases", rep.Releases(),
		"blobs_freed", rep.BlobsDeleted, "blobs_kept", rep.BlobsRetained, "bytes_freed", rep.ReclaimableBytes)
}
