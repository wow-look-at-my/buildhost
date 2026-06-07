package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/retention"
)

const maxRetentionBody = 1 << 16

// apiRetention (GET /api/retention) returns the current policy plus a dry-run
// preview of exactly what enforcing now would evict.
func (s *Server) apiRetention(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings, err := s.db.GetRetentionSettings(ctx)
	if err != nil {
		s.retentionError(w, r, err)
		return
	}
	preview, err := retention.New(s.db, s.store, retention.ConfigFromSettings(settings, false)).Plan(ctx)
	if err != nil {
		s.retentionError(w, r, err)
		return
	}
	s.writeJSON(w, s.retentionResponse(settings, preview))
}

// apiUpdateRetention (PUT /api/retention) persists a new policy, then returns it
// with a fresh preview.
func (s *Server) apiUpdateRetention(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		KeepN        *int `json:"keep_n"`
		RecencyHours *int `json:"recency_hours"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRetentionBody)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.KeepN == nil || body.RecencyHours == nil {
		http.Error(w, "keep_n and recency_hours are required", http.StatusBadRequest)
		return
	}
	// keep_n=0 still keeps each branch's tip (enforced in the query). Bounds keep
	// a fat-fingered value from being absurd; recency caps at 10 years.
	if *body.KeepN < 0 || *body.KeepN > 100000 || *body.RecencyHours < 0 || *body.RecencyHours > 87600 {
		http.Error(w, "keep_n must be 0..100000 and recency_hours 0..87600", http.StatusBadRequest)
		return
	}
	if err := s.db.UpdateRetentionSettings(ctx, *body.KeepN, *body.RecencyHours); err != nil {
		s.retentionError(w, r, err)
		return
	}
	settings, err := s.db.GetRetentionSettings(ctx)
	if err != nil {
		s.retentionError(w, r, err)
		return
	}
	preview, err := retention.New(s.db, s.store, retention.ConfigFromSettings(settings, false)).Plan(ctx)
	if err != nil {
		s.retentionError(w, r, err)
		return
	}
	s.writeJSON(w, s.retentionResponse(settings, preview))
}

// apiRunRetention (POST /api/retention/run) runs GC now. Body {enforce: bool};
// report-only unless enforce is true.
func (s *Server) apiRunRetention(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Enforce bool `json:"enforce"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRetentionBody)).Decode(&body)

	settings, err := s.db.GetRetentionSettings(ctx)
	if err != nil {
		s.retentionError(w, r, err)
		return
	}
	rep, err := retention.New(s.db, s.store, retention.ConfigFromSettings(settings, body.Enforce)).Run(ctx)
	if err != nil {
		s.retentionError(w, r, err)
		return
	}
	if body.Enforce {
		slog.Warn("retention run via admin dashboard",
			"releases", rep.Releases(), "blobs_freed", rep.BlobsDeleted, "bytes_freed", rep.ReclaimableBytes)
	}
	s.writeJSON(w, reportJSON(rep))
}

func (s *Server) retentionError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("admin api error", "err", err, "path", r.URL.Path)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (s *Server) retentionResponse(settings db.RetentionSettings, preview retention.Report) map[string]any {
	return map[string]any{
		"keep_n":        settings.KeepN,
		"recency_hours": settings.RecencyHours,
		// The background sweeper is deploy-level config the dashboard cannot change.
		"sweeper_enabled": s.cfg.RetentionInterval > 0,
		"sweeper_enforce": s.cfg.RetentionEnforce,
		"preview":         reportJSON(preview),
	}
}

func reportJSON(rep retention.Report) map[string]any {
	releases := make([]map[string]any, 0, rep.Releases())
	add := func(refs []retention.ReleaseRef, reason string) {
		for _, ref := range refs {
			releases = append(releases, map[string]any{
				"project_name": ref.ProjectName,
				"project_id":   ref.ProjectID,
				"branch":       ref.Branch,
				"version":      ref.Version,
				"reason":       reason,
			})
		}
	}
	add(rep.EvictedReleases, "keep-n")
	add(rep.AbandonedReleases, "abandoned")
	return map[string]any{
		"enforced":          rep.Enforced,
		"release_count":     rep.Releases(),
		"keep_n_count":      len(rep.EvictedReleases),
		"abandoned_count":   len(rep.AbandonedReleases),
		"blobs":             rep.BlobsDeleted,
		"blobs_retained":    rep.BlobsRetained,
		"reclaimable_bytes": rep.ReclaimableBytes,
		"releases":          releases,
	}
}
