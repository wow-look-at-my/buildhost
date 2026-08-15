package goproxy

import (
	"context"
	"time"
)

// State is everything the admin dashboard shows about the proxy. It is
// assembled here rather than in internal/admin so the dashboard cannot drift
// from what the proxy actually does.
type State struct {
	Health  Health   `json:"health"`
	Cache   Cache    `json:"cache"`
	Traffic Traffic  `json:"traffic"`
	Modules []Module `json:"modules"`
	Recent  []Event  `json:"recent"`
}

// Cache is what the proxy is holding, from the database (survives a restart).
type Cache struct {
	Modules        int64 `json:"modules"`
	Versions       int64 `json:"versions"`
	Zips           int64 `json:"zips"`
	Bytes          int64 `json:"bytes"`
	FailingModules int64 `json:"failing_modules"`
}

// Traffic is process-lifetime counters. Labelled since_start because they are
// in memory: see the note on metrics in goproxy.go.
type Traffic struct {
	SinceStart bool             `json:"since_start"`
	CacheHits  int64            `json:"cache_hits"`
	CacheMiss  int64            `json:"cache_misses"`
	Fetches    int64            `json:"fetches"`
	BytesSent  int64            `json:"bytes_sent"`
	Errors     map[string]int64 `json:"errors"`
}

// Module is one cached module's row on the dashboard. LastErrorKind is the
// field that makes a credential failure visible: a module stuck on
// "unauthorized" is the exact state that otherwise looks like a healthy proxy.
type Module struct {
	Path          string `json:"path"`
	Source        string `json:"source"`
	Private       bool   `json:"private"`
	Versions      int64  `json:"versions"`
	Bytes         int64  `json:"bytes"`
	LastErrorKind string `json:"last_error_kind"`
	LastError     string `json:"last_error"`
	LastErrorAt   string `json:"last_error_at,omitempty"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
	LastFetchedAt string `json:"last_fetched_at,omitempty"`
}

// Snapshot assembles the dashboard state.
func (s *Service) Snapshot(ctx context.Context) (State, error) {
	st := State{Health: s.Health(), Modules: []Module{}, Recent: []Event{}}

	stats, err := s.db.GoproxyCacheStats(ctx)
	if err != nil {
		return st, err
	}
	st.Cache = Cache{
		Modules:        stats.Modules,
		Versions:       stats.Versions,
		Zips:           stats.Zips,
		Bytes:          toInt64(stats.Bytes),
		FailingModules: stats.FailingModules,
	}

	hits, misses, fetches, bytes, errs, recent := s.metrics.snapshot()
	st.Traffic = Traffic{
		SinceStart: true,
		CacheHits:  hits,
		CacheMiss:  misses,
		Fetches:    fetches,
		BytesSent:  bytes,
		Errors:     errs,
	}
	st.Recent = recent

	rows, err := s.db.ListGoproxyModules(ctx)
	if err != nil {
		return st, err
	}
	for _, r := range rows {
		m := Module{
			Path:          r.ModulePath,
			Source:        r.Source,
			Private:       s.isPrivate(r.ModulePath),
			Versions:      r.VersionCount,
			Bytes:         toInt64(r.Bytes),
			LastErrorKind: r.LastErrorKind,
			LastError:     r.LastError,
		}
		if r.LastErrorAt != nil {
			m.LastErrorAt = r.LastErrorAt.UTC().Format("2006-01-02 15:04:05Z")
		}
		if r.LastSuccessAt != nil {
			m.LastSuccessAt = r.LastSuccessAt.UTC().Format("2006-01-02 15:04:05Z")
		}
		m.LastFetchedAt = formatAny(r.LastFetchedAt)
		st.Modules = append(st.Modules, m)
	}
	return st, nil
}

// CheckHealthNow re-runs the readiness probe on demand, so the dashboard's
// recheck button reports the credential's state right now rather than up to the
// poll interval ago.
func (s *Service) CheckHealthNow(ctx context.Context) Health { return s.checkHealth(ctx) }

// Config exposes the resolved configuration for the dashboard.
func (s *Service) Config() Config { return s.cfg }

// toInt64 narrows the interface{} SQLite returns for SUM()/COALESCE columns.
func toInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

// formatAny renders a MAX(datetime) column, which SQLite hands back as a string
// or as time.Time depending on the driver's inference.
func formatAny(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format("2006-01-02 15:04:05Z")
	default:
		return ""
	}
}
