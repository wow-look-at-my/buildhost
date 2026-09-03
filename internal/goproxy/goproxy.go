// Package goproxy serves the Go module download protocol from buildhost,
// replacing a separately-deployed Athens.
package goproxy

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// Subdomain is the service label this backend answers on: goproxy.{domain}.
const Subdomain = "goproxy"

const defaultUpstream = ""

// Config is resolved from the environment at registration time.
type Config struct {
	// PrivatePrefixes are module path prefixes fetched direct from GitHub with
	PrivatePrefixes []string
	// Upstream is the public module mirror. Empty disables passthrough, leaving
	Upstream        string
	ReadinessModule string
}

func loadConfig(orgs []string) Config {
	c := Config{Upstream: defaultUpstream}

	if v := strings.TrimSpace(os.Getenv("BUILDHOST_GOPROXY_PRIVATE_PREFIXES")); v != "" {
		for _, p := range strings.Split(v, ",") {
			if p = strings.Trim(strings.TrimSpace(p), "/"); p != "" {
				c.PrivatePrefixes = append(c.PrivatePrefixes, p)
			}
		}
	} else {
		for _, org := range orgs {
			if org = strings.TrimSpace(org); org != "" {
				c.PrivatePrefixes = append(c.PrivatePrefixes, "github.com/"+org)
			}
		}
	}
	if v := strings.TrimSpace(os.Getenv("BUILDHOST_GOPROXY_UPSTREAM")); v != "" {
		c.Upstream = strings.TrimSuffix(v, "/")
	}
	c.ReadinessModule = strings.TrimSpace(os.Getenv("BUILDHOST_GOPROXY_READINESS_MODULE"))
	return c
}

// Service holds the proxy's dependencies and its observable state.
type Service struct {
	cfg      Config
	db       *db.DB
	store    storage.Storage
	github   *githubSource
	upstream *upstreamSource

	metrics *metrics
	health  *health

	// single-flights concurrent fetches of the same module version, so a cold
	inflight singleflight
}

func newService(cfg Config, database *db.DB, store storage.Storage, dataDir string) *Service {
	client := &http.Client{Timeout: 2 * time.Minute}
	return &Service{
		cfg:      cfg,
		db:       database,
		store:    store,
		github:   newGitHubSource(client, dataDir),
		upstream: newUpstreamSource(client, cfg.Upstream, cfg.PrivatePrefixes),
		metrics:  newMetrics(),
		health:   newHealth(),
	}
}

// isPrivate reports whether a module is served from GitHub direct rather than
// the public mirror.
func (s *Service) isPrivate(modPath string) bool {
	return matchesPrefix(modPath, s.cfg.PrivatePrefixes)
}

var (
	startOnce sync.Once
	service   *Service
	serviceMu sync.RWMutex
)

// Current returns the running Service, or nil before auth.Init. The admin
// dashboard reads proxy state through it.
func Current() *Service {
	serviceMu.RLock()
	defer serviceMu.RUnlock()
	return service
}

// Routes register in init(), NOT inside the OnReady callback below. An OnReady
// callback fires only from auth.Init (server startup), so routes registered
// there are invisible to `buildhost routes`, to the route-diff CI check, and to
func init() {
	registerRoutes()
	auth.OnReady(func() {
		startOnce.Do(func() {
			s := newService(loadConfig(auth.OIDCOrgs()), auth.DB(), auth.Store(), auth.DataDir())
			serviceMu.Lock()
			service = s
			serviceMu.Unlock()
			s.startReadiness(context.Background())
		})
	})
}

// metrics are process-lifetime counters. They are deliberately in memory and
// reported as "since start": persisting a counter per request would put a write
type metrics struct {
	mu sync.Mutex

	CacheHits    int64
	CacheMisses  int64
	Fetches      int64
	Errors       map[string]int64
	BytesServed  int64
	recent       []Event
	recentCursor int
}

type Event struct {
	At       time.Time `json:"at"`
	Module   string    `json:"module"`
	Version  string    `json:"version"`
	Endpoint string    `json:"endpoint"`
	Source   string    `json:"source"`
	Outcome  string    `json:"outcome"`
	Status   int       `json:"status"`
	Detail   string    `json:"detail"`
	Duration string    `json:"duration"`
}

const recentEvents = 200

func newMetrics() *metrics {
	return &metrics{Errors: map[string]int64{}, recent: make([]Event, 0, recentEvents)}
}

func (m *metrics) record(e Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch e.Outcome {
	case "hit":
		m.CacheHits++
	case "fetch":
		m.CacheMisses++
		m.Fetches++
	case "error":
		m.Errors[e.Detail]++
	}
	if len(m.recent) < recentEvents {
		m.recent = append(m.recent, e)
		return
	}
	m.recent[m.recentCursor] = e
	m.recentCursor = (m.recentCursor + 1) % recentEvents
}

func (m *metrics) addBytes(n int64) {
	m.mu.Lock()
	m.BytesServed += n
	m.mu.Unlock()
}

func (m *metrics) snapshot() (hits, misses, fetches, bytes int64, errs map[string]int64, recent []Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	errs = make(map[string]int64, len(m.Errors))
	for k, v := range m.Errors {
		errs[k] = v
	}
	recent = make([]Event, 0, len(m.recent))
	for i := len(m.recent) - 1; i >= 0; i-- {
		idx := (m.recentCursor + i) % len(m.recent)
		recent = append(recent, m.recent[idx])
	}
	return m.CacheHits, m.CacheMisses, m.Fetches, m.BytesServed, errs, recent
}

// singleflight collapses concurrent identical fetches.
type singleflight struct {
	mu sync.Mutex
	m  map[string]*flightCall
}

type flightCall struct {
	done chan struct{}
	err  error
}

func (s *singleflight) do(key string, fn func() error) error {
	s.mu.Lock()
	if s.m == nil {
		s.m = map[string]*flightCall{}
	}
	if c, ok := s.m[key]; ok {
		s.mu.Unlock()
		<-c.done
		return c.err
	}
	c := &flightCall{done: make(chan struct{})}
	s.m[key] = c
	s.mu.Unlock()

	c.err = fn()
	close(c.done)

	s.mu.Lock()
	delete(s.m, key)
	s.mu.Unlock()
	return c.err
}

func logAttrs(e *Error) []any {
	return []any{
		"kind", e.Kind.String(),
		"module", e.Module,
		"version", e.Version,
		"upstream", e.Upstream,
		"upstream_status", e.UpstreamStatus,
		"detail", e.Detail,
	}
}

func logFailure(e *Error) {
	// An authorization failure is an operator problem, not a caller problem: it
	if e.Kind == KindUnauthorized || e.Kind == KindUpstream {
		slog.Error("goproxy fetch failed", logAttrs(e)...)
		return
	}
	slog.Info("goproxy fetch failed", logAttrs(e)...)
}
