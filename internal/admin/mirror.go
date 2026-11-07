// apiMirror lists what the registry mirror holds, and when the janitor deletes each blob unless a pull reads it again.

package admin

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/diskusage"
	"github.com/wow-look-at-my/buildhost/internal/mirror"
)

type mirrorBlobRow struct {
	Upstream  string    `json:"upstream"`
	Digest    string    `json:"digest"`
	MediaType string    `json:"media_type"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
	UsedAt    time.Time `json:"used_at"`
	Reads     int64     `json:"reads"`
	ExpiresAt time.Time `json:"expires_at"`
}

type mirrorTagRow struct {
	Upstream   string    `json:"upstream"`
	Repository string    `json:"repository"`
	Tag        string    `json:"tag"`
	Digest     string    `json:"digest"`
	MediaType  string    `json:"media_type"`
	CheckedAt  time.Time `json:"checked_at"`
}

type mirrorUpstreamRow struct {
	Service string `json:"service"`
	Remote  string `json:"remote"`
}

// apiMirror lists what the registry mirror holds, and when the janitor deletes each blob unless a pull reads it again.
func (s *Server) apiMirror(w http.ResponseWriter, r *http.Request) {
	blobs, err := s.db.ListMirrorBlobs(r.Context())
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	tags, err := s.db.ListMirrorTags(r.Context())
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	blobRows := make([]mirrorBlobRow, len(blobs))
	var total int64
	for idx, b := range blobs {
		blobRows[len(blobs)-1-idx] = mirrorBlobRow{
			Upstream: b.Upstream, Digest: b.Digest, MediaType: b.MediaType, Size: b.Size,
			CreatedAt: b.CreatedAt, UsedAt: b.UsedAt, Reads: b.Reads, ExpiresAt: b.UsedAt.Add(s.cfg.MirrorKeep),
		}
		total += b.Size
	}
	tagRows := make([]mirrorTagRow, len(tags))
	for idx, t := range tags {
		tagRows[idx] = mirrorTagRow{Upstream: t.Upstream, Repository: t.Repository, Tag: t.Tag, Digest: t.Digest, MediaType: t.MediaType, CheckedAt: t.CheckedAt}
	}
	upstreams := make([]mirrorUpstreamRow, len(mirror.Upstreams))
	for idx, u := range mirror.Upstreams {
		upstreams[idx] = mirrorUpstreamRow{Service: u.Service, Remote: u.Remote}
	}
	resp := map[string]any{
		"keep_seconds":           int64(s.cfg.MirrorKeep / time.Second),
		"check_interval_seconds": int64(mirror.CheckInterval / time.Second),
		"tag_ttl_seconds":        int64(mirror.TagTTL / time.Second),
		"min_free_percent":       s.cfg.MirrorMinFreePercent,
		"total_bytes":            total,
		"upstreams":              upstreams,
		"blobs":                  blobRows,
		"tags":                   tagRows,
		"janitor":                mirror.LastReport(),
	}
	if du, err := diskusage.Get(s.cfg.DataDir); err == nil && du.Total > 0 {
		resp["disk_free"] = du.Free
		resp["disk_total"] = du.Total
		resp["disk_used"] = du.Used
	}
	s.writeJSON(w, resp)
}
