package admin

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/diskusage"
)

type runFileRow struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

type runFilesRun struct {
	Repo      string       `json:"repo"`
	RunID     string       `json:"run_id"`
	CreatedAt time.Time    `json:"created_at"`
	UsedAt    time.Time    `json:"used_at"`
	EndedAt   *time.Time   `json:"ended_at"`
	ExpiresAt *time.Time   `json:"expires_at"`
	Bytes     int64        `json:"bytes"`
	Files     []runFileRow `json:"files"`
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
			Repo: run.RepoPath, RunID: run.RunID, CreatedAt: run.CreatedAt, UsedAt: run.UsedAt,
			EndedAt: run.EndedAt, Bytes: run.Bytes, Files: make([]runFileRow, 0, len(files)),
		}
		if run.EndedAt != nil {
			expires := run.EndedAt.Add(s.cfg.RunFilesKeep)
			row.ExpiresAt = &expires
		}
		for _, f := range files {
			row.Files = append(row.Files, runFileRow{Name: f.Name, Size: f.Size, SHA256: f.StorageKey, CreatedAt: f.CreatedAt})
		}
		total += run.Bytes
		out = append(out, row)
	}
	resp := map[string]any{
		"keep_seconds":     int64(s.cfg.RunFilesKeep / time.Second),
		"min_free_percent": s.cfg.RunFilesMinFreePercent,
		"total_bytes":      total,
		"runs":             out,
	}
	if du, err := diskusage.Get(s.cfg.DataDir); err == nil && du.Total > 0 {
		resp["disk_free"] = du.Free
		resp["disk_total"] = du.Total
	}
	s.writeJSON(w, resp)
}
