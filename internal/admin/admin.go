package admin

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

//go:embed static/*
var content embed.FS

type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	RepoURL string `json:"-"`
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

	cpuMu      sync.Mutex
	cpuPercent float64
	cpuTotal   time.Duration

	staticFS  fs.FS
	indexHTML []byte
}

func New(cfg config.Config, database *db.DB, build BuildInfo) *Server {
	staticFS, _ := fs.Sub(content, "static")
	indexHTML, _ := fs.ReadFile(staticFS, "index.html")

	s := &Server{
		cfg:       cfg,
		db:        database,
		build:     build,
		startTime: time.Now(),
		staticFS:  staticFS,
		indexHTML:  indexHTML,
	}
	s.cpuTotal = getCPUTime()
	return s
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

func (s *Server) NewHTTPServer() *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/sidebar", s.apiSidebar)
	mux.HandleFunc("GET /api/dashboard", s.apiDashboard)
	mux.HandleFunc("GET /api/projects/{name}/releases/{version}", s.apiRelease)
	mux.HandleFunc("GET /api/projects/{name}", s.apiProject)
	mux.HandleFunc("GET /api/projects", s.apiProjects)
	mux.HandleFunc("GET /api/registries", s.apiRegistries)
	mux.HandleFunc("GET /api/tokens", s.apiTokens)
	mux.HandleFunc("GET /api/oidc", s.apiOIDC)
	mux.HandleFunc("GET /api/sites", s.apiSites)
	mux.HandleFunc("GET /admin/inflight", InflightHandler)

	mux.HandleFunc("GET /", s.serveSPA)

	s.startCPUTracker()

	return &http.Server{
		Addr:              s.cfg.AdminListenAddr,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

func (s *Server) ListenAndServe() error {
	return s.NewHTTPServer().ListenAndServe()
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self' data:")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) serveSPA(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}
	if f, err := s.staticFS.Open(path[1:]); err == nil {
		f.Close()
		http.ServeFileFS(w, r, s.staticFS, path[1:])
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(s.indexHTML)
}

func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode json", "err", err)
	}
}

func (s *Server) apiSidebar(w http.ResponseWriter, r *http.Request) {
	s.cpuMu.Lock()
	cpuPct := s.cpuPercent
	s.cpuMu.Unlock()

	resp := map[string]any{
		"build": map[string]any{
			"version":      s.build.Version,
			"commit":       s.build.Commit,
			"commit_url":   s.build.CommitURL(),
			"short_commit": s.build.ShortCommit(),
			"date":         s.build.Date,
		},
		"build_age":   s.buildAge(),
		"cpu_percent": fmt.Sprintf("%.1f%%", cpuPct),
	}

	if du, err := getDiskUsage(s.cfg.DataDir); err == nil && du.Total > 0 {
		resp["disk_used"] = humanSize(int64(du.Used))
		resp["disk_total"] = humanSize(int64(du.Total))
	}

	s.writeJSON(w, resp)
}

func (s *Server) apiDashboard(w http.ResponseWriter, r *http.Request) {
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
	if recent == nil {
		recent = []db.RecentRelease{}
	}

	s.cpuMu.Lock()
	cpuPct := s.cpuPercent
	cpuTotal := s.cpuTotal
	s.cpuMu.Unlock()

	s.writeJSON(w, map[string]any{
		"stats":  stats,
		"recent": recent,
		"config": map[string]any{
			"base_url":           s.cfg.BaseURL,
			"listen_addr":        s.cfg.ListenAddr,
			"admin_listen_addr":  s.cfg.AdminListenAddr,
			"data_dir":           s.cfg.DataDir,
			"oidc_issuers":       s.cfg.OIDCIssuers,
			"oidc_orgs":          s.cfg.OIDCOrgs,
			"oidc_events":        s.cfg.OIDCEvents,
		},
		"build": map[string]any{
			"version":      s.build.Version,
			"commit":       s.build.Commit,
			"commit_url":   s.build.CommitURL(),
			"short_commit": s.build.ShortCommit(),
			"date":         s.build.Date,
		},
		"uptime":      formatDuration(time.Since(s.startTime)),
		"cpu_percent": fmt.Sprintf("%.1f%%", cpuPct),
		"cpu_total":   formatDuration(cpuTotal),
		"disk_bytes":  blobsDiskUsage(s.cfg.DataDir + "/blobs"),
	})
}

func (s *Server) apiProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.ListProjectSummaries(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if projects == nil {
		projects = []db.ProjectSummary{}
	}
	s.writeJSON(w, projects)
}

func (s *Server) apiProject(w http.ResponseWriter, r *http.Request) {
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
	if releases == nil {
		releases = []db.ReleaseSummary{}
	}

	sites, err := s.db.ListSites(ctx, project.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]any{
		"project":  project,
		"releases": releases,
		"sites":    sites,
		"base_url": s.cfg.BaseURL,
	})
}

func (s *Server) apiRelease(w http.ResponseWriter, r *http.Request) {
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

	rows, pkgs, err := s.db.ListArtifactDetails(ctx, release.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	totalDownloads, err := s.db.GetTotalDownloads(ctx, release.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type artifactView struct {
		db.ListArtifactDetailsWithDownloadsRow
		Packages []db.ListPackagedFormatsRow `json:"packages"`
	}
	artifacts := make([]artifactView, len(rows))
	var totalSize int64
	for i, a := range rows {
		artifacts[i] = artifactView{ListArtifactDetailsWithDownloadsRow: a, Packages: pkgs[i]}
		totalSize += a.Size
	}

	s.writeJSON(w, map[string]any{
		"project":         project,
		"release":         release,
		"artifacts":       artifacts,
		"total_downloads": totalDownloads,
		"total_size":      totalSize,
		"base_url":        s.cfg.BaseURL,
	})
}

func (s *Server) apiRegistries(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.ListProjectSummaries(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if projects == nil {
		projects = []db.ProjectSummary{}
	}

	s.writeJSON(w, map[string]any{
		"base_url": s.cfg.BaseURL,
		"projects": projects,
	})
}

func (s *Server) apiTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.db.ListTokenDetails(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type tokenResp struct {
		Name        string     `json:"name"`
		TokenPrefix string     `json:"token_prefix"`
		IsGlobal    bool       `json:"is_global"`
		ProjectName string     `json:"project_name"`
		Scopes      string     `json:"scopes"`
		IsExpired   bool       `json:"is_expired"`
		CreatedAt   time.Time  `json:"created_at"`
		LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
		ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	}

	resp := make([]tokenResp, 0, len(tokens))
	for _, t := range tokens {
		resp = append(resp, tokenResp{
			Name:        t.Name,
			TokenPrefix: t.TokenPrefix,
			IsGlobal:    t.IsGlobal(),
			ProjectName: t.ProjectName,
			Scopes:      t.Scopes,
			IsExpired:   t.IsExpired(),
			CreatedAt:   t.CreatedAt,
			LastUsedAt:  t.LastUsedAt,
			ExpiresAt:   t.ExpiresAt,
		})
	}
	s.writeJSON(w, resp)
}

func (s *Server) apiSites(w http.ResponseWriter, r *http.Request) {
	sites, err := s.db.ListSiteDetails(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sites == nil {
		sites = []db.SiteDetail{}
	}
	s.writeJSON(w, map[string]any{
		"sites":    sites,
		"base_url": s.cfg.BaseURL,
	})
}

func (s *Server) apiOIDC(w http.ResponseWriter, r *http.Request) {
	policies, err := s.db.ListOIDCPolicyDetails(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if policies == nil {
		policies = []db.OIDCPolicyDetail{}
	}
	s.writeJSON(w, policies)
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
