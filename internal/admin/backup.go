package admin

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// apiBackup (GET /api/backup) streams a consistent snapshot of the live
// database as a file download. The blob store is not included.
func (s *Server) apiBackup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// The snapshot goes beside the database: the same filesystem is known.
	dir, err := os.MkdirTemp(filepath.Dir(s.cfg.DBPath), "backup-")
	if err != nil {
		slog.ErrorContext(ctx, "backup failed: temp dir", "error", err)
		http.Error(w, "backup failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(dir)

	snap := filepath.Join(dir, "buildhost.db")
	if err := s.db.SnapshotTo(ctx, snap); err != nil {
		slog.ErrorContext(ctx, "backup failed: snapshot", "error", err)
		http.Error(w, "backup failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	f, err := os.Open(snap)
	if err != nil {
		http.Error(w, "backup failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "backup failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	name := fmt.Sprintf("buildhost-%s.db", time.Now().UTC().Format("20060102T150405Z"))
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Cache-Control", "no-store")
	if _, err := io.Copy(w, f); err != nil {
		slog.ErrorContext(ctx, "backup download interrupted", "error", err)
		return
	}
	slog.InfoContext(ctx, "database backup downloaded", "bytes", info.Size())
}
