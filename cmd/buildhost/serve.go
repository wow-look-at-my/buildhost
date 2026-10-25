package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/KimMachineGun/automemlimit/memlimit"
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/buildhost/internal/admin"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/brew"
	"github.com/wow-look-at-my/buildhost/internal/buildinfo"
	"github.com/wow-look-at-my/buildhost/internal/cicache"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/diskusage"
	"github.com/wow-look-at-my/buildhost/internal/recompress"
	"github.com/wow-look-at-my/buildhost/internal/retention"
	"github.com/wow-look-at-my/buildhost/internal/runfiles"
	"github.com/wow-look-at-my/buildhost/internal/scratch"
	"github.com/wow-look-at-my/buildhost/internal/server"
	"github.com/wow-look-at-my/buildhost/internal/sites"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/buildhost/internal/telemetry"
	"github.com/wow-look-at-my/buildhost/internal/uploads"
)

func init() {
	rootCmd.AddCommand(serveCmd)
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the registry server",
	RunE: func(_ *cobra.Command, _ []string) error {
		cfg := config.Load()
		// Refuse to start on a configuration that cannot serve correctly, rather
		// than degrade silently at request time.
		if err := cfg.Validate(); err != nil {
			return err
		}

		// Make the Go runtime aware of the container's memory cgroup. Do this
		// so the GC runs harder as we approach the limit instead of letting the
		// heap grow
		if limit, err := memlimit.SetGoMemLimitWithOpts(
			memlimit.WithRatio(0.9),
			memlimit.WithProvider(memlimit.FromCgroup),
			memlimit.WithLogger(slog.Default()),
		); err != nil {
			slog.Warn("could not set GOMEMLIMIT from cgroup", "err", err)
		} else if limit > 0 {
			slog.Info("set GOMEMLIMIT from cgroup", "bytes", limit)
		}

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
		// The spool directories for in-progress uploads stay on the data
		// volume; everything else scratch goes to TMPDIR.
		if err := os.MkdirAll(cfg.DataDir+"/tmp", 0o755); err != nil {
			return fmt.Errorf("create temp dir: %w", err)
		}
		scratch.Configure(cfg.TempDir)
		reclaimed := sweepScratch(cfg)
		slog.Info("scratch root resolved", "dir", scratch.Root(), "reclaimed_bytes", reclaimed)

		recomp := recompress.NewFromSettings(recompress.ConfigFrom(cfg))
		recompress.Set(recomp)
		defer recomp.Close()

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer database.Close()

		if err := database.SeedRetentionSettings(context.Background(), cfg.RetentionKeepN, int(cfg.RetentionRecencyGuard.Hours())); err != nil {
			return fmt.Errorf("seed retention settings: %w", err)
		}

		fsStore, err := storage.NewFilesystem(cfg.DataDir+"/blobs", cfg.StorageCompress)
		if err != nil {
			return fmt.Errorf("init storage: %w", err)
		}
		store := storage.NewTraced(fsStore)

		if err := sites.ConvertTarSites(context.Background(), database, store, scratch.Root()); err != nil {
			slog.Error("sites: tar conversion incomplete", "err", err)
		}

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
		// One background worker hashes what the brew tap history still lacks, so no tap request has to.
		go func() {
			if err := brew.BackfillHistoryDigests(ctx); err != nil {
				slog.Error("brew: tap history digest backfill incomplete", "err", err)
			}
		}()
		slog.Info("starting server", "addr", cfg.ListenAddr)

		go func() {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				errc <- fmt.Errorf("main server: %w", err)
			}
		}()

		startRetentionSweeper(ctx, cfg, database, store)
		// Reclaim leftover scratch for as long as the process runs.
		go runScratchJanitor(ctx, cfg)
		// Sweep expired chunked-upload sessions (abandoned spool files).
		uploads.StartJanitor(ctx)
		(&runfiles.Janitor{
			DB:             database,
			Store:          store,
			DataDir:        cfg.DataDir,
			Keep:           cfg.RunFilesKeep,
			MinFreePercent: cfg.RunFilesMinFreePercent,
			RunEnded:       auth.GitHubRunEnded,
			Disk:           diskusage.Get,
			Busy:           func() bool { return admin.InflightWrites() > 0 },
		}).Start(ctx)
		(&cicache.Janitor{
			DB:             database,
			Store:          store,
			DataDir:        cfg.DataDir,
			Keep:           cfg.CacheKeep,
			MinFreePercent: cfg.CacheMinFreePercent,
			Disk:           diskusage.Get,
			Busy:           func() bool { return admin.InflightWrites() > 0 },
		}).Start(ctx)

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

// sweepScratch reclaims orphaned temporary files from the active scratch root
// and from the {DataDir}/tmp an earlier version wrote. Downloads no longer
// create anything there, so a backlog left by that version is reclaimed with
// no operator action.
func sweepScratch(cfg config.Config) scratch.SweepReport {
	return scratchJanitor(cfg).Run()
}

func scratchJanitor(cfg config.Config) *scratch.Janitor {
	return &scratch.Janitor{
		Root:      scratch.Root(),
		LegacyDir: filepath.Join(cfg.DataDir, "tmp"),
		CacheDir:  filepath.Join(scratch.Root(), scratch.CacheDirName),
		StaleAge:  cfg.ScratchAge,
	}
}

// runScratchJanitor keeps reclaiming leftover scratch for as long as the
// process runs. Only files past the stale age are eligible, so a request in
// flight is never touched.
func runScratchJanitor(ctx context.Context, cfg config.Config) {
	jan := scratchJanitor(cfg)
	t := time.NewTicker(sweepEvery(cfg.ScratchAge))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			jan.Run()
		}
	}
}

// sweepEvery picks how often to sweep, well inside the age that makes a file
// orphaned.
func sweepEvery(staleAge time.Duration) time.Duration {
	every := staleAge / 2
	if every < 10*time.Minute {
		every = 10 * time.Minute
	}
	if every > time.Hour {
		every = time.Hour
	}
	return every
}

// startRetentionSweeper launches the background GC sweeper when a positive
// interval is configured. It is report-only unless BUILDHOST_RETENTION_ENFORCE is
// set, defers a tick. This holds while writes are in flight (the same counter
// /ready-to-update uses), and exits when ctx is cancelled at shutdown.
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
				settings, err := database.GetRetentionSettings(ctx)
				if err != nil {
					slog.Error("retention sweep: load settings failed", "err", err)
					continue
				}
				ret := retention.New(database, store, retention.ConfigFromSettings(settings, cfg.RetentionEnforce)).
					WithRecordDeleter(recordDeleterFor(cfg))
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
		"blobs_freed", rep.BlobsDeleted, "blobs_kept", rep.BlobsRetained, "bytes_freed", rep.ReclaimableBytes,
		"records_marked_deleted", rep.RecordsMarkedDeleted, "records_unmarked", rep.RecordsUnmarked)

	// Every unmarked record is the org's linked artifacts page claiming
	// buildhost still holds something. The sweeper cannot fail
	if rep.RecordsUnmarked > 0 {
		slog.Warn("retention: evicted artifacts still recorded as stored",
			"records", rep.RecordsUnmarked, "errors", strings.Join(rep.RecordErrors, "; "))
	}
}
