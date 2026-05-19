package server

import (
	"net/http"
	"time"

	_ "github.com/wow-look-at-my/buildhost/internal/api"
	_ "github.com/wow-look-at-my/buildhost/internal/apt"
	_ "github.com/wow-look-at-my/buildhost/internal/brew"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	_ "github.com/wow-look-at-my/buildhost/internal/dl"
	_ "github.com/wow-look-at-my/buildhost/internal/npm"
	_ "github.com/wow-look-at-my/buildhost/internal/oci"
	"github.com/wow-look-at-my/buildhost/internal/storage"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

type Server struct {
	cfg config.Config
}

func New(cfg config.Config, database *db.DB, store storage.Storage) *Server {
	auth.Init(database, store, cfg.BaseURL)
	return &Server{cfg: cfg}
}

func (s *Server) ListenAndServe() error {
	srv := &http.Server{
		Addr:    s.cfg.ListenAddr,
		Handler: s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return srv.ListenAndServe()
}

func (s *Server) Handler() http.Handler {
	mux := auth.Mux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	var h http.Handler = mux
	h = auth.GetMiddleware().Authenticate(h)
	h = securityHeaders(h)
	h = loggingMiddleware(h)
	h = recoveryMiddleware(h)
	return h
}
