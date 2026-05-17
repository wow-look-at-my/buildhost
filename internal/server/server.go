package server

import (
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/api"
	"github.com/wow-look-at-my/buildhost/internal/apt"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/brew"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/dl"
	npmhandler "github.com/wow-look-at-my/buildhost/internal/npm"
	"github.com/wow-look-at-my/buildhost/internal/oci"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

type Server struct {
	cfg          config.Config
	db           *db.DB
	store        storage.Storage
	authMW       *auth.Middleware
	apiHandler   *api.Handler
	dlHandler    *dl.Handler
	aptHandler   *apt.Handler
	brewHandler  *brew.Handler
	npmHandler   *npmhandler.Handler
	ociHandler   *oci.Handler
	orchestrator *repackage.Orchestrator
}

func New(cfg config.Config, database *db.DB, store storage.Storage) *Server {
	orchestrator := repackage.NewOrchestrator(store, database, cfg.BaseURL)
	return &Server{
		cfg:          cfg,
		db:           database,
		store:        store,
		authMW:       &auth.Middleware{DB: database},
		apiHandler:   &api.Handler{DB: database, Store: store, Orchestrator: orchestrator},
		dlHandler:    &dl.Handler{DB: database, Store: store},
		aptHandler:   &apt.Handler{DB: database, Store: store},
		brewHandler:  &brew.Handler{DB: database, Store: store},
		npmHandler:   &npmhandler.Handler{DB: database, Store: store, BaseURL: cfg.BaseURL},
		ociHandler:   &oci.Handler{DB: database, Store: store},
		orchestrator: orchestrator,
	}
}

func (s *Server) ListenAndServe() error {
	return http.ListenAndServe(s.cfg.ListenAddr, s.Handler())
}

func (s *Server) Handler() http.Handler {
	mux := s.routes()
	var handler http.Handler = mux
	handler = s.authMW.Authenticate(handler)
	handler = loggingMiddleware(handler)
	handler = recoveryMiddleware(handler)
	return handler
}
