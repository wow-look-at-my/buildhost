package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/admin"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/buildinfo"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/buildhost/internal/uploads"
)

var healthDB *db.DB

// The update-check contract docker-updater probes on the container's own port,
const (
	wellKnownHealth    = "/.well-known/docker-updater/health"
	wellKnownPreUpdate = "/.well-known/docker-updater/pre-update"
)

// healthResponse is the JSON body of GET /healthz. It always reports the build
// the server is running (commit and version) so a deploy can be verified
// without a dedicated version endpoint; status is "ok" only when the database
// is reachable.
type healthResponse struct {
	Status   string `json:"status"` // "ok" when healthy, "unhealthy" otherwise
	Commit   string `json:"commit"` // git SHA the binary was built from, or "unknown"
	Version  string `json:"version"`
	Modified bool   `json:"modified,omitempty"` // built from a dirty working tree
	Error    string `json:"error,omitempty"`    // failure detail when unhealthy
}

type Server struct {
	cfg config.Config
	srv *http.Server
}

func New(cfg config.Config, database *db.DB, store storage.Storage) *Server {
	auth.Init(database, store, cfg.DataDir, cfg.OIDCIssuers, cfg.OIDCOrgs, cfg.OIDCEvents, cfg.SiteFetchDomains, cfg.GitHubWebhookSecret, cfg.GitHubClientID, cfg.GitHubClientSecret, cfg.SiteDomain, cfg.PrimaryDomain)
	auth.SetGitHubToken(cfg.GitHubToken)
	if err := auth.SetGitHubApp(cfg.GitHubAppID, cfg.GitHubAppPrivateKey); err != nil {
		// Non-fatal: default-branch lookups fall back to the PAT/anonymous path.
		slog.Error("GitHub App auth disabled (default-branch lookups degrade to PAT/anonymous)", "err", err)
	}
	healthDB = database

	health := func(w http.ResponseWriter, r *http.Request) {
		resp := healthResponse{
			Status:   "ok",
			Commit:   buildinfo.Commit(),
			Version:  buildinfo.Version(),
			Modified: buildinfo.Get().Modified,
		}
		code := http.StatusOK
		if err := healthDB.PingContext(r.Context()); err != nil {
			resp.Status = "unhealthy"
			resp.Error = "database unreachable"
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(resp)
	}
	readyToUpdate := func(w http.ResponseWriter, _ *http.Request) {
		if admin.InflightWrites() > 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}

	auth.HandleRaw("GET /healthz", health)
	auth.HandleRaw("GET /ready-to-update", readyToUpdate)

	auth.HandleRaw("GET "+wellKnownHealth, health)
	auth.HandleRaw("GET "+wellKnownPreUpdate, readyToUpdate)

	s := &Server{cfg: cfg}
	s.srv = &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
	return s
}

func (s *Server) ListenAndServe() error {
	return s.srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func (s *Server) Handler() http.Handler {
	var h http.Handler = http.HandlerFunc(auth.ServeHTTP)
	// Between Authenticate (needs the token in context to bind sessions to
	h = uploads.ResolveSessionBody(h)
	h = auth.GetMiddleware().Authenticate(h)
	h = admin.TrackInflight(h)
	h = securityHeaders(h)
	h = loggingMiddleware(h)
	h = recoveryMiddleware(h)
	h = tracingMiddleware(h)
	return h
}
