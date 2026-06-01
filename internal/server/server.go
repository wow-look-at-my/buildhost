package server

import (
	"context"
	"net/http"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/admin"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var healthDB *db.DB

type Server struct {
	cfg config.Config
	srv *http.Server
}

func New(cfg config.Config, database *db.DB, store storage.Storage) *Server {
	auth.Init(database, store, cfg.BaseURL, cfg.DataDir, cfg.OIDCIssuers, cfg.OIDCOrgs, cfg.OIDCEvents, cfg.SiteFetchDomains)
	healthDB = database

	auth.HandleRaw("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := healthDB.PingContext(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("database unreachable"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
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
