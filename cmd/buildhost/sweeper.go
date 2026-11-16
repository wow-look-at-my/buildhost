// The retention sweeper, which a saved setting starts, stops or retimes with no restart.

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/admin"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/retention"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// startRetentionSweeper runs the background GC sweeper. It reads the interval
// and the enforce flag from the configuration in use, so a saved setting
// starts, stops or retimes it. wake reports a changed interval. A tick waits
// while writes are in flight, the same counter /ready-to-update uses.
func startRetentionSweeper(ctx context.Context, database *db.DB, store storage.Storage, wake <-chan struct{}) {
	go func() {
		last := time.Duration(-1)
		for {
			cfg := config.Current()
			if cfg.RetentionInterval != last {
				last = cfg.RetentionInterval
				if last > 0 {
					slog.Info("retention sweeper enabled", "interval", last, "enforce", cfg.RetentionEnforce)
				} else {
					slog.Info("retention sweeper disabled")
				}
			}
			var tick <-chan time.Time
			var timer *time.Timer
			if last > 0 {
				timer = time.NewTimer(last)
				tick = timer.C
			}
			select {
			case <-ctx.Done():
				return
			case <-wake:
				if timer != nil {
					timer.Stop()
				}
				continue
			case <-tick:
			}
			retentionSweep(ctx, config.Current(), database, store)
		}
	}()
}

func retentionSweep(ctx context.Context, cfg config.Config, database *db.DB, store storage.Storage) {
	if n := admin.InflightWrites(); n > 0 {
		slog.Info("retention: deferring sweep, writes in flight", "inflight", n)
		return
	}
	// The Retention page edits this policy, so read it for each sweep.
	settings, err := database.GetRetentionSettings(ctx)
	if err != nil {
		slog.Error("retention sweep: load settings failed", "err", err)
		return
	}
	ret := retention.New(database, store, retention.ConfigFromSettings(settings, cfg.RetentionEnforce)).
		WithRecordDeleter(recordDeleterFor(cfg))
	rep, err := ret.Run(ctx)
	if err != nil {
		slog.Error("retention sweep failed", "err", err)
		return
	}
	logRetentionReport(rep)
}
