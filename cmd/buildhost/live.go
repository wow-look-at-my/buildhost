// The parts of the running server that a saved setting changes with no restart.

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/admin"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/buildinfo"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/retention"
	"github.com/wow-look-at-my/buildhost/internal/secretserver"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/buildhost/internal/telemetry"
)

// liveTelemetry points OTLP export at the configured endpoint. An empty
// endpoint stops the export.
type liveTelemetry struct {
	endpoint string
	base     *slog.Logger
	stop     func(context.Context) error
}

func (t *liveTelemetry) apply(endpoint string) error {
	if endpoint == t.endpoint {
		return nil
	}
	if t.base == nil {
		t.base = slog.Default()
	}
	t.shutdown()
	if endpoint == "" {
		slog.Info("telemetry disabled")
		return nil
	}
	stop, err := telemetry.Init(context.Background(), endpoint, buildinfo.Version())
	if err != nil {
		return err
	}
	t.endpoint, t.stop = endpoint, stop
	slog.Info("telemetry enabled", "endpoint", endpoint)
	return nil
}

// shutdown flushes the export and gives the log back to stderr only.
func (t *liveTelemetry) shutdown() {
	if t.stop == nil {
		return
	}
	slog.SetDefault(t.base)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := t.stop(ctx); err != nil {
		slog.Error("telemetry shutdown failed", "endpoint", t.endpoint, "err", err)
	}
	t.endpoint, t.stop = "", nil
}

// liveGitHub holds the credential for buildhost's own GitHub calls.
type liveGitHub struct {
	ctx     context.Context
	applied bool
	in      [4]string
	stop    context.CancelFunc
}

func (g *liveGitHub) apply(cfg config.Config) error {
	in := [4]string{cfg.GitHubToken, cfg.GitHubAppID, cfg.GitHubAppPrivateKey, cfg.SecretServerToken}
	if g.applied && in == g.in {
		return nil
	}
	g.applied, g.in = true, in
	if g.stop != nil {
		g.stop()
		g.stop = nil
	}
	secretserver.ClearStatus()
	auth.SetGitHubToken(cfg.GitHubToken)
	if err := auth.SetGitHubApp(cfg.GitHubAppID, cfg.GitHubAppPrivateKey); err != nil {
		slog.Error("GitHub App auth disabled (default-branch lookups degrade to PAT/anonymous)", "err", err)
	}
	if p := cfg.GitHubAppProblem(); p != "" {
		slog.Error("GitHub App credential ignored; fix it on the admin Settings page", "problem", p)
	}
	switch {
	case cfg.SecretServerToken == "":
		slog.Error("no BUILDHOST_SECRET_SERVER_TOKEN: buildhost gets no GitHub credential from secret-server", "github_credential", auth.GitHubCredentialSource())
	case cfg.GitHubToken != "" || cfg.HasGitHubApp():
		slog.Warn("secret-server: the GitHub credential in the settings takes precedence; the machine token is not used")
	default:
		ctx, cancel := context.WithCancel(g.ctx)
		src := &secretserver.Source{URL: secretserver.URL, Token: cfg.SecretServerToken}
		if err := src.Start(ctx); err != nil {
			cancel()
			return err
		}
		g.stop = cancel
	}
	return nil
}

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
