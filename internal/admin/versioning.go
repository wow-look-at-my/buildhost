package admin

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

// apiSetVersioning changes a project's versioning scheme. Existing releases
// keep their numbers. An auto project continues from the highest version_num.
func (s *Server) apiSetVersioning(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Versioning string `json:"versioning"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	v := db.Versioning(req.Versioning)
	if v != db.VersioningAuto && v != db.VersioningSemver {
		http.Error(w, `versioning must be "auto" or "semver"`, http.StatusBadRequest)
		return
	}
	project, err := s.db.GetProject(ctx, r.PathValue("name"))
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("admin set versioning", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if project.Versioning != v {
		if err := s.db.SetProjectVersioning(ctx, project.ID, v); err != nil {
			slog.Error("admin set versioning", "err", err, "project", project.Name)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		slog.Warn("project versioning changed", "project", project.Name, "from", project.Versioning, "to", v)
	}
	s.writeJSON(w, map[string]any{"name": project.Name, "versioning": v})
}
