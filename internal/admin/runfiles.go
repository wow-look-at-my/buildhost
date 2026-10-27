// State is "live", "ended" or "unknown".

package admin

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/diskusage"
	"github.com/wow-look-at-my/buildhost/internal/runfiles"
)

type runFileRow struct {
	Name      string     `json:"name"`
	Size      int64      `json:"size"`
	SHA256    string     `json:"sha256"`
	CreatedAt time.Time  `json:"created_at"`
	Reads     int64      `json:"reads"`
	ReadAt    *time.Time `json:"read_at"`
}

type runFilesRun struct {
	Repo      string     `json:"repo"`
	RunID     string     `json:"run_id"`
	CreatedAt time.Time  `json:"created_at"`
	UsedAt    time.Time  `json:"used_at"`
	CheckedAt *time.Time `json:"checked_at"`
	// State is "live", "ended" or "unknown". Unknown means GitHub could not report the run's status.
	State      string       `json:"state"`
	CheckError string       `json:"check_error,omitempty"`
	EndedAt    *time.Time   `json:"ended_at"`
	ExpiresAt  *time.Time   `json:"expires_at"`
	Bytes      int64        `json:"bytes"`
	Reads      int64        `json:"reads"`
	Files      []runFileRow `json:"files"`
}

// apiRunFiles lists every CI run that holds run files, its files, and when
// the run files janitor deletes them. A live run has no expiry.
func (s *Server) apiRunFiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runs, err := s.db.RunStorageRuns(ctx)
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]runFilesRun, 0, len(runs))
	var total int64
	for _, run := range runs {
		files, err := s.db.ListRunFiles(ctx, db.RunKey{RepoID: run.RepoID, RunID: run.RunID})
		if err != nil {
			slog.Error("admin api error", "err", err, "path", r.URL.Path)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		row := runFilesRun{
			Repo: run.RepoPath, RunID: run.RunID, CreatedAt: run.CreatedAt, UsedAt: run.UsedAt, CheckedAt: run.CheckedAt,
			EndedAt: run.EndedAt, Bytes: run.Bytes, Files: make([]runFileRow, 0, len(files)),
		}
		row.State = "live"
		if run.EndedAt != nil {
			row.State = "ended"
			expires := run.EndedAt.Add(s.cfg.RunFilesKeep)
			row.ExpiresAt = &expires
		} else if msg := runfiles.CheckError(db.RunKey{RepoID: run.RepoID, RunID: run.RunID}); msg != "" {
			// The janitor deletes such a run once nothing used it for the keep period.
			row.State = "unknown"
			row.CheckError = msg
			expires := run.UsedAt.Add(s.cfg.RunFilesKeep)
			row.ExpiresAt = &expires
		}
		for _, f := range files {
			row.Files = append(row.Files, runFileRow{Name: f.Name, Size: f.Size, SHA256: f.StorageKey, CreatedAt: f.CreatedAt, Reads: f.Reads, ReadAt: f.ReadAt})
			row.Reads += f.Reads
		}
		total += run.Bytes
		out = append(out, row)
	}
	resp := map[string]any{
		"keep_seconds":           int64(s.cfg.RunFilesKeep / time.Second),
		"check_interval_seconds": int64(runfiles.CheckInterval / time.Second),
		"min_free_percent":       s.cfg.RunFilesMinFreePercent,
		"total_bytes":            total,
		"runs":                   out,
		"janitor":                runfiles.LastReport(),
	}
	if du, err := diskusage.Get(s.cfg.DataDir); err == nil && du.Total > 0 {
		resp["disk_free"] = du.Free
		resp["disk_total"] = du.Total
		resp["disk_used"] = du.Used
	}
	s.writeJSON(w, resp)
}
