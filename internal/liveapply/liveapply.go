// Package liveapply holds the parts of the running server that a saved setting
// replaces with no restart: OTLP export and the GitHub credential.
package liveapply

import (
	"context"
	"log/slog"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/buildinfo"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/secretserver"
	"github.com/wow-look-at-my/buildhost/internal/telemetry"
)

// Telemetry points OTLP export at the configured endpoint. An empty endpoint
// stops the export.
type Telemetry struct {
	endpoint string
	base     *slog.Logger
	stop     func(context.Context) error
}

// Apply puts endpoint in use. The same endpoint changes nothing.
func (t *Telemetry) Apply(endpoint string) error {
	if endpoint == t.endpoint {
		return nil
	}
	if t.base == nil {
		t.base = slog.Default()
	}
	t.Shutdown()
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

// Shutdown flushes the export and gives the log back to stderr only.
func (t *Telemetry) Shutdown() {
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

// GitHub holds the credential for buildhost's own GitHub calls.
type GitHub struct {
	// Ctx ends the secret-server fetch loop.
	Ctx     context.Context
	applied bool
	in      [4]string
	stop    context.CancelFunc
}

// Apply puts the credential of cfg in use. The same credential settings change
// nothing, so an unrelated save keeps a credential that secret-server vended.
func (g *GitHub) Apply(cfg config.Config) error {
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
		ctx, cancel := context.WithCancel(g.Ctx)
		src := &secretserver.Source{URL: secretserver.URL, Token: cfg.SecretServerToken}
		if err := src.Start(ctx); err != nil {
			cancel()
			return err
		}
		g.stop = cancel
	}
	return nil
}
