// apiRetention (GET /api/retention) returns the current policy plus a dry-run preview of exactly what enforcing now would evict.

package admin

import (
	"encoding/json"
	"fmt"
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

// apiRetentionInventory (GET /api/retention/inventory) returns every stored
// file with the reason retention keeps it. It is the debug view behind the
// reclaimable number: a total says how little comes back, this says what holds
// the rest. It reads only, like the preview.
func (s *Server) apiRetentionInventory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings, err := s.db.GetRetentionSettings(ctx)
	if err != nil {
		s.retentionError(w, r, err)
		return
	}
	inv, err := retention.New(s.db, s.store, retention.ConfigFromSettings(settings, false)).Inventory(ctx)
	if err != nil {
		s.retentionError(w, r, err)
		return
	}
	s.writeJSON(w, inv)
}

// apiRunRetention (POST /api/retention/run) runs GC now. Body {enforce: bool};
// report-only unless enforce is true.
func (s *Server) apiRunRetention(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Enforce bool `json:"enforce"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRetentionBody)).Decode(&body)

	// Same guard as the background sweeper.
	if body.Enforce {
		if n := InflightWrites(); n > 0 {
			http.Error(w, fmt.Sprintf("%d write(s) in flight; retry when idle", n), http.StatusConflict)
			return
		}
	}

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

// refChecker answers which branches GitHub still holds. Tests replace it.
var refChecker retention.RefChecker = retention.GitHubRefChecker{}

// apiDeletedBranches (GET /api/retention/deleted-branches) previews the
// deleted-branch pass: every non-default branch build, with what GitHub said
// about its branch.
func (s *Server) apiDeletedBranches(w http.ResponseWriter, r *http.Request) {
	s.deletedBranches(w, r, false)
}

// apiRunDeletedBranches (POST /api/retention/deleted-branches) deletes every
// build older than the recency guard on a non-default branch that GitHub
// confirms is gone.
func (s *Server) apiRunDeletedBranches(w http.ResponseWriter, r *http.Request) {
	if n := InflightWrites(); n > 0 {
		http.Error(w, fmt.Sprintf("%d write(s) in flight; retry when idle", n), http.StatusConflict)
		return
	}
	s.deletedBranches(w, r, true)
}

func (s *Server) deletedBranches(w http.ResponseWriter, r *http.Request, enforce bool) {
	ctx := r.Context()
	settings, err := s.db.GetRetentionSettings(ctx)
	if err != nil {
		s.retentionError(w, r, err)
		return
	}
	rep, err := retention.New(s.db, s.store, retention.ConfigFromSettings(settings, enforce)).RunDeletedBranches(ctx, refChecker)
	if err != nil {
		s.retentionError(w, r, err)
		return
	}
	if enforce {
		slog.Warn("deleted-branch builds removed via admin dashboard",
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
	add(rep.DeletedBranchReleases, "deleted-branch")
	branches := make([]map[string]any, 0, len(rep.BranchChecks))
	for _, c := range rep.BranchChecks {
		branches = append(branches, map[string]any{
			"project_name": c.ProjectName,
			"github_repo":  c.GithubRepo,
			"branch":       c.Branch,
			"state":        c.State,
			"error":        c.Error,
			"releases":     c.Releases,
		})
	}
	return map[string]any{
		"branches":          branches,
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
