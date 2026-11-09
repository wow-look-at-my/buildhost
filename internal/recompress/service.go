// Config is the process-wide cache and throttle settings.

package recompress

import (
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/scratch"
)

// Config is the process-wide cache and throttle settings.
type Config struct {
	CacheLimit   int64
	CacheEntries int
	CacheTTL     time.Duration
	PerIP        int
	Window       time.Duration
	LoadCeiling  float64
}

// Service owns the response cache and the throttle.
type Service struct {
	Cache   *Cache
	Limiter *Limiter
	sampler *Sampler
}

// NewService builds the service against tmpDir. It returns nil when the cache
// cannot be created, so a download still works with recompression uncached.
func NewService(cfg Config, tmpDir string) *Service {
	s := NewSampler(0, nil)
	s.Start()
	cache, err := NewCache(tmpDir, cfg.CacheLimit, cfg.CacheEntries, cfg.CacheTTL)
	if err != nil {
		slog.Error("recompress: cache unavailable", "error", err)
	}
	if cache != nil {
		slog.Info("recompression cache ready",
			"dir", cache.dir, "bytes", cfg.CacheLimit, "entries", cfg.CacheEntries, "ttl", cfg.CacheTTL)
	}
	limiter := NewLimiter(LimiterConfig{PerIP: cfg.PerIP, Window: cfg.Window, LoadCeiling: cfg.LoadCeiling}, s.Load)
	slog.Info("recompression throttle ready",
		"per_ip", cfg.PerIP, "window", cfg.Window, "load_ceiling", cfg.LoadCeiling,
		"load", s.Load(), "allowance", limiter.Allowance())
	return &Service{Cache: cache, Limiter: limiter, sampler: s}
}

// Close stops the load sampler.
func (s *Service) Close() {
	if s != nil && s.sampler != nil {
		s.sampler.Stop()
	}
}

// ConfigFrom reads the download-transform settings out of the server config.
func ConfigFrom(cfg config.Config) Config {
	return Config{
		CacheLimit:   cfg.RecompressCacheLimit,
		CacheEntries: cfg.RecompressEntryLimit,
		CacheTTL:     cfg.RecompressTTL,
		PerIP:        cfg.RecompressPerIP,
		Window:       cfg.RecompressWindow,
		LoadCeiling:  cfg.RecompressLoadCeiling,
	}
}

var (
	sharedMu sync.Mutex
	shared   *Service
)

// Set stores the process-wide service.
func Set(s *Service) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	previous := shared
	shared = s
	if previous != nil && previous != s {
		previous.Close()
	}
}

// Get returns the process-wide service, or nil when none was configured.
func Get() *Service {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	return shared
}

// NewFromSettings builds the service from already-resolved settings.
func NewFromSettings(cfg Config) *Service {
	return NewService(cfg, filepath.Join(scratch.Root(), scratch.CacheDirName))
}
