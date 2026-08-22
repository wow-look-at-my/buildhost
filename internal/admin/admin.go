package admin

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/router"
)

// The dashboard's JS is GENERATED from internal/admin/frontend/src (TypeScript);
// the .js files are gitignored build artifacts, never committed. Each generated
// file is embedded BY NAME on purpose: a wildcard still matches index.html and
// style.css, so a build that skipped generate would compile clean and serve a
// blank dashboard -- naming them makes that a compile error instead.
//
//go:generate ../../scripts/build-admin-frontend.sh
//go:embed static/index.html static/style.css static/app.js static/copy.js
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
	store     storage.Storage
	build     BuildInfo
	startTime time.Time

	cpuTrackerOnce sync.Once
	cpuMu          sync.Mutex
	cpuPercent     float64
	cpuTotal       time.Duration

	staticFS  fs.FS
	indexHTML []byte
}

func New(cfg config.Config, database *db.DB, store storage.Storage, build BuildInfo) *Server {
	staticFS, _ := fs.Sub(content, "static")
	indexHTML, _ := fs.ReadFile(staticFS, "index.html")

	s := &Server{
		cfg:       cfg,
		db:        database,
		store:     store,
		build:     build,
		startTime: time.Now(),
		staticFS:  staticFS,
		indexHTML: indexHTML,
	}
	s.cpuTotal = getCPUTime()
	return s
}

// startCPUTracker spawns the background sampling goroutine at most once per
// Server: NewHTTPServer can be called repeatedly (every admin test request
// does), and without this guard each call leaked another ticker goroutine
// forever.
func (s *Server) startCPUTracker() {
	s.cpuTrackerOnce.Do(func() {
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
	})
}

func (s *Server) NewHTTPServer() *http.Server {
	mux := router.New()

	mux.HandleFunc("GET /api/sidebar", router.Allow, s.apiSidebar)
	mux.HandleFunc("GET /api/dashboard", router.Allow, s.apiDashboard)
	mux.HandleFunc("GET /api/projects/{name}/releases/{version}", router.Allow, s.apiRelease)
	mux.HandleFunc("GET /api/projects/{name}/downloads", router.Allow, s.apiProjectDownloads)
	mux.HandleFunc("POST /api/projects/{name}/download-links", router.Allow, s.apiCreateDownloadLink)
	mux.HandleFunc("GET /api/projects/{name}", router.Allow, s.apiProject)
	mux.HandleFunc("GET /api/projects", router.Allow, s.apiProjects)
	mux.HandleFunc("GET /api/registries", router.Allow, s.apiRegistries)
	mux.HandleFunc("GET /api/tokens", router.Allow, s.apiTokens)
	mux.HandleFunc("POST /api/tokens", router.Allow, s.apiCreateToken)
	mux.HandleFunc("PATCH /api/tokens/{id}", router.Allow, s.apiUpdateToken)
	mux.HandleFunc("DELETE /api/tokens/{id}", router.Allow, s.apiDeleteToken)
	mux.HandleFunc("GET /api/oidc", router.Allow, s.apiOIDC)
	mux.HandleFunc("GET /api/sites", router.Allow, s.apiSites)
	mux.HandleFunc("GET /api/artifacts", router.Allow, s.apiArtifacts)
	mux.HandleFunc("GET /api/storage", router.Allow, s.apiStorage)
	mux.HandleFunc("GET /api/goproxy", router.Allow, s.apiGoproxy)
	mux.HandleFunc("POST /api/goproxy/recheck", router.Allow, s.apiGoproxyRecheck)
	mux.HandleFunc("GET /api/retention", router.Allow, s.apiRetention)
	mux.HandleFunc("PUT /api/retention", router.Allow, s.apiUpdateRetention)
	mux.HandleFunc("GET /api/retention/inventory", router.Allow, s.apiRetentionInventory)
	mux.HandleFunc("POST /api/retention/run", router.Allow, s.apiRunRetention)
	mux.HandleFunc("GET /admin/inflight", router.Allow, InflightHandler)

	mux.HandleFunc("GET /{path...}", router.Allow, s.serveSPA)

	s.startCPUTracker()

	return &http.Server{
		Addr:              s.cfg.AdminListenAddr,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
}

func (s *Server) ListenAndServe() error {
	return s.NewHTTPServer().ListenAndServe()
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		// The admin SPA is entirely first-party: it ships inline event handlers
		// (onclick) and inline styles in the markup it builds. 'unsafe-inline'
		// permits that own code to run -- without it the page's edit/delete
		// buttons (script-src-attr) silently do nothing. It does NOT relax the
		// origin allowlist: cross-origin scripts/styles/connections are still
		// confined to 'self' and data:, so injected third-party scripts (e.g. a
		// Cloudflare analytics beacon) remain blocked.
		w.Header().Set("Content-Security-Policy", "default-src 'self' data: 'unsafe-inline'")
		w.Header().Set("X-Permitted-Cross-Domain-Policies", "none")
		w.Header().Set("Permissions-Policy", "interest-cohort=()")
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

// serviceURLs returns the public base URL of every registry service, each on
// its own subdomain, derived from the incoming request Host.
//
// The registry serves each format from a dedicated subdomain (dl., apt., brew.,
// npm., oci., sites., static.) -- never from a path prefix on the main host.
// The admin dashboard itself runs on a subdomain (e.g. admin.example.com), so
// auth.DeriveServiceURL strips that first label and rebuilds the real service
// host (dl.example.com, ...). These are exactly the hosts the router matches,
// because they are produced by the same helpers the main server uses when it
// emits cross-service links, so the dashboard can never drift from reality.
func serviceURLs(r *http.Request) map[string]string {
	return map[string]string{
		"dl":     auth.DeriveServiceURL(r, "dl").String(),
		"apt":    auth.DeriveServiceURL(r, "apt").String(),
		"brew":   auth.DeriveServiceURL(r, "brew").String(),
		"npm":    auth.DeriveServiceURL(r, "npm").String(),
		"oci":    auth.DeriveServiceURL(r, "oci").String(),
		"sites":  auth.DeriveServiceURL(r, "sites").String(),
		"static": auth.DeriveServiceURL(r, "static").String(),
	}
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
