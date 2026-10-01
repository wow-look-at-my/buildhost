package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// Bounds on how many recent download events apiProjectDownloads returns.
const (
	downloadsDefaultLimit = 200
	downloadsMaxLimit     = 1000
)

// apiProjectDownloads (GET /api/projects/{name}/downloads) returns the project's
// most recent download-attribution events -- who fetched which artifact and
func (s *Server) apiProjectDownloads(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")

	project, err := s.db.GetProject(ctx, name)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	limit := int64(downloadsDefaultLimit)
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil && n > 0 {
			limit = n
		}
	}
	if limit > downloadsMaxLimit {
		limit = downloadsMaxLimit
	}

	events, err := s.db.ListDownloadEventsByProject(ctx, project.ID, limit)
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]any{
		"project":   project,
		"downloads": events,
		"limit":     limit,
		"base_url":  auth.RequestRootURL(r),
		"services":  serviceURLs(r),
	})
}
