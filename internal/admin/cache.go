package admin

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/cicache"
	"github.com/wow-look-at-my/buildhost/internal/diskusage"
)

type cacheEntryRow struct {
	Repo      string    `json:"repo"`
	Ref       string    `json:"ref"`
	Key       string    `json:"key"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
	UsedAt    time.Time `json:"used_at"`
	Reads     int64     `json:"reads"`
	ExpiresAt time.Time `json:"expires_at"`
}

// apiCache lists every CI cache entry, most recently used first, and when the
// cache janitor deletes each unless a restore uses it again.
func (s *Server) apiCache(w http.ResponseWriter, r *http.Request) {
	entries, err := s.db.ListCacheEntries(r.Context())
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]cacheEntryRow, len(entries))
	var total int64
	for idx, e := range entries {
		out[len(entries)-1-idx] = cacheEntryRow{
			Repo: e.RepoPath, Ref: e.Ref, Key: e.Key, Size: e.Size, SHA256: e.StorageKey,
			CreatedAt: e.CreatedAt, UsedAt: e.UsedAt, Reads: e.Reads, ExpiresAt: e.UsedAt.Add(s.cfg.CacheKeep),
		}
		total += e.Size
	}
	resp := map[string]any{
		"keep_seconds":           int64(s.cfg.CacheKeep / time.Second),
		"check_interval_seconds": int64(cicache.CheckInterval / time.Second),
		"min_free_percent":       s.cfg.CacheMinFreePercent,
		"total_bytes":            total,
		"entries":                out,
		"janitor":                cicache.LastReport(),
	}
	if du, err := diskusage.Get(s.cfg.DataDir); err == nil && du.Total > 0 {
		resp["disk_free"] = du.Free
		resp["disk_total"] = du.Total
		resp["disk_used"] = du.Used
	}
	s.writeJSON(w, resp)
}
