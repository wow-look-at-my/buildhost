package server

import (
	"net/http"
	"sync"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var healthzOnce sync.Once

type Server struct {
	cfg config.Config
}

func New(cfg config.Config, database *db.DB, store storage.Storage) *Server {
	auth.Init(database, store, cfg.BaseURL)
	return &Server{cfg: cfg}
}

func (s *Server) ListenAndServe() error {
	srv := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       120 * time.Second,
	}
	return srv.ListenAndServe()
}

func (s *Server) Handler() http.Handler {
	mux := auth.Mux()
	healthzOnce.Do(func() {
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})
	})
	var h http.Handler = mux
	h = auth.GetMiddleware().Authenticate(h)
	h = securityHeaders(h)
	h = loggingMiddleware(h)
	h = recoveryMiddleware(h)
	return h
}
