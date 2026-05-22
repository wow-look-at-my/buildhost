package admin

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

//go:embed templates/*.html static/*
var content embed.FS

type BuildInfo struct {
	Version string
	Commit  string
	Date    string
	RepoURL string
}

func (b BuildInfo) CommitURL() string {
	if b.Commit == "" || b.Commit == "none" || b.RepoURL == "" {
		return ""
	}
	return b.RepoURL + "/commit/" + b.Commit
}

func (b BuildInfo) ShortCommit() string {
	if len(b.Commit) > 12 {
		return b.Commit[:12]
	}
	return b.Commit
}

type Server struct {
	cfg       config.Config
	db        *db.DB
	build     BuildInfo
	startTime time.Time
	templates map[string]*template.Template

	cpuMu      sync.Mutex
	cpuPercent float64
	cpuTotal   time.Duration
}

func New(cfg config.Config, database *db.DB, build BuildInfo) *Server {
	s := &Server{
		cfg:       cfg,
		db:        database,
		build:     build,
		startTime: time.Now(),
	}
	s.loadTemplates()
	s.cpuTotal = getCPUTime()
	return s
}

func (s *Server) loadTemplates() {
	fm := template.FuncMap{
		"humanSize":     humanSize,
		"timeAgo":       timeAgo,
		"formatTime":    formatTime,
		"formatTimePtr": formatTimePtr,
		"dedupRatio":    dedupRatio,
	}

	s.templates = make(map[string]*template.Template)
	for _, page := range []string{"dashboard", "projects", "project", "release", "registries", "tokens", "oidc"} {
		s.templates[page] = template.Must(
			template.New("").Funcs(fm).ParseFS(content,
				"templates/layout.html",
				"templates/"+page+".html",
			),
		)
	}
}

func (s *Server) startCPUTracker() {
	prev := getCPUTime()
	prevWall := time.Now()
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			curr := getCPUTime()
			wall := time.Now()
			elapsed := wall.Sub(prevWall)
			if elapsed > 0 {
				pct := float64(curr-prev) / float64(elapsed) * 100
				s.cpuMu.Lock()
				s.cpuPercent = pct
				s.cpuTotal = curr
				s.cpuMu.Unlock()
			}
			prev = curr
			prevWall = wall
		}
	}()
}

func (s *Server) ListenAndServe() error {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", http.FileServerFS(content))

	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /projects", s.handleProjects)
	mux.HandleFunc("GET /projects/{name}", s.handleProject)
	mux.HandleFunc("GET /projects/{name}/releases/{version}", s.handleRelease)
	mux.HandleFunc("GET /registries", s.handleRegistries)
	mux.HandleFunc("GET /tokens", s.handleTokens)
	mux.HandleFunc("GET /oidc", s.handleOIDCPolicies)

	s.startCPUTracker()

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

func (s *Server) sidebarData() map[string]any {
	s.cpuMu.Lock()
	cpuPct := s.cpuPercent
	s.cpuMu.Unlock()

	sb := map[string]any{
		"Build":      s.build,
		"BuildAge":   s.buildAge(),
		"CPUPercent": fmt.Sprintf("%.1f%%", cpuPct),
	}

	if du, err := getDiskUsage(s.cfg.DataDir); err == nil && du.Total > 0 {
		sb["DiskUsed"] = humanSize(int64(du.Used))
		sb["DiskTotal"] = humanSize(int64(du.Total))
	}

	return sb
}

func (s *Server) buildAge() string {
	if s.build.Date == "" {
		return ""
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s.build.Date); err == nil {
			return timeAgo(t)
		}
	}
	return s.build.Date
}

func (s *Server) render(w http.ResponseWriter, name string, data map[string]any) {
	tmpl, ok := s.templates[name]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	data["Sidebar"] = s.sidebarData()
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

	s.cpuMu.Lock()
	cpuPct := s.cpuPercent
	cpuTotal := s.cpuTotal
	s.cpuMu.Unlock()

	s.render(w, "dashboard", map[string]any{
		"Nav":        "dashboard",
		"Stats":      stats,
		"Recent":     recent,
		"Config":     s.cfg,
		"Build":      s.build,
		"Uptime":     formatDuration(time.Since(s.startTime)),
		"CPUPercent": fmt.Sprintf("%.1f%%", cpuPct),
		"CPUTotal":   formatDuration(cpuTotal),
		"DiskBytes":  blobsDiskUsage(s.cfg.DataDir + "/blobs"),
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

func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")
	version := r.PathValue("version")

	project, err := s.db.GetProject(ctx, name)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	release, err := s.db.GetRelease(ctx, project.ID, version)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	artifacts, err := s.db.ListArtifactDetails(ctx, release.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	totalDownloads, err := s.db.GetTotalDownloads(ctx, release.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var totalSize int64
	for _, a := range artifacts {
		totalSize += a.Size
	}

	s.render(w, "release", map[string]any{
		"Nav":            "projects",
		"Project":        project,
		"Release":        release,
		"Artifacts":      artifacts,
		"TotalDownloads": totalDownloads,
		"TotalSize":      totalSize,
		"BaseURL":        s.cfg.BaseURL,
	})
}

func (s *Server) handleRegistries(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.ListProjectSummaries(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.render(w, "registries", map[string]any{
		"Nav":      "registries",
		"BaseURL":  s.cfg.BaseURL,
		"Projects": projects,
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

func blobsDiskUsage(dir string) int64 {
	var total int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

func dedupRatio(logical, physical int64) string {
	if physical == 0 {
		return "1.0x"
	}
	ratio := float64(logical) / float64(physical)
	return fmt.Sprintf("%.1fx", ratio)
}

func formatDuration(d time.Duration) string {
	d = d.Truncate(time.Second)
	if d < time.Second {
		return "0s"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	return fmt.Sprintf("%dm %ds", minutes, seconds)
}
