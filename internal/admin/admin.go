package admin

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

//go:embed templates/*.html static/*
var content embed.FS

type Server struct {
	cfg       config.Config
	db        *db.DB
	templates map[string]*template.Template
}

func New(cfg config.Config, database *db.DB) *Server {
	s := &Server{cfg: cfg, db: database}
	s.loadTemplates()
	return s
}

func (s *Server) loadTemplates() {
	fm := template.FuncMap{
		"humanSize":     humanSize,
		"timeAgo":       timeAgo,
		"formatTime":    formatTime,
		"formatTimePtr": formatTimePtr,
	}

	s.templates = make(map[string]*template.Template)
	for _, page := range []string{"dashboard", "projects", "project", "tokens", "oidc"} {
		s.templates[page] = template.Must(
			template.New("").Funcs(fm).ParseFS(content,
				"templates/layout.html",
				"templates/"+page+".html",
			),
		)
	}
}

func (s *Server) ListenAndServe() error {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", http.FileServerFS(content))

	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /projects", s.handleProjects)
	mux.HandleFunc("GET /projects/{name}", s.handleProject)
	mux.HandleFunc("GET /tokens", s.handleTokens)
	mux.HandleFunc("GET /oidc", s.handleOIDCPolicies)

	srv := &http.Server{
		Addr:              s.cfg.AdminListenAddr,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return srv.ListenAndServe()
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) render(w http.ResponseWriter, name string, data map[string]any) {
	tmpl, ok := s.templates[name]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		slog.Error("render template", "name", name, "err", err)
	}
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	stats, err := s.db.GetDashboardStats(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	recent, err := s.db.ListRecentReleases(ctx, 10)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.render(w, "dashboard", map[string]any{
		"Nav":    "dashboard",
		"Stats":  stats,
		"Recent": recent,
		"Config": s.cfg,
	})
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.ListProjectSummaries(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.render(w, "projects", map[string]any{
		"Nav":      "projects",
		"Projects": projects,
	})
}

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")

	project, err := s.db.GetProject(ctx, name)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	releases, err := s.db.ListReleaseSummaries(ctx, project.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.render(w, "project", map[string]any{
		"Nav":      "projects",
		"Project":  project,
		"Releases": releases,
	})
}

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.db.ListTokenDetails(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.render(w, "tokens", map[string]any{
		"Nav":    "tokens",
		"Tokens": tokens,
	})
}

func (s *Server) handleOIDCPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := s.db.ListOIDCPolicyDetails(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.render(w, "oidc", map[string]any{
		"Nav":      "oidc",
		"Policies": policies,
	})
}

func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return formatTime(*t)
}
