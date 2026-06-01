package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/admin"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/buildinfo"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var healthDB *db.DB

// healthResponse is the JSON body of GET /healthz. It always reports the build
// the server is running (commit and version) so a deploy can be verified
// without a dedicated version endpoint; status is "ok" only when the database
// is reachable.
type healthResponse struct {
	Status   string `json:"status"`             // "ok" when healthy, "unhealthy" otherwise
	Commit   string `json:"commit"`             // git SHA the binary was built from, or "unknown"
	Version  string `json:"version"`            // synthetic build version (v0.0.<unix> or "dev")
	Modified bool   `json:"modified,omitempty"` // built from a dirty working tree
	Error    string `json:"error,omitempty"`    // failure detail when unhealthy
}

type Server struct {
	cfg config.Config
	srv *http.Server
}

func New(cfg config.Config, database *db.DB, store storage.Storage) *Server {
	auth.Init(database, store, cfg.BaseURL, cfg.DataDir, cfg.OIDCIssuers, cfg.OIDCOrgs, cfg.OIDCEvents, cfg.SiteFetchDomains)
	healthDB = database

	auth.HandleRaw("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
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
	})
	auth.HandleRaw("GET /ready-to-update", func(w http.ResponseWriter, _ *http.Request) {
		if admin.InflightWrites() > 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

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
	h = auth.GetMiddleware().Authenticate(h)
	h = admin.TrackInflight(h)
	h = securityHeaders(h)
	h = loggingMiddleware(h)
	h = recoveryMiddleware(h)
	h = tracingMiddleware(h)
	return h
}
