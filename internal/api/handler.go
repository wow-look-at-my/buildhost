package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.DB = auth.DB()
		handler.Store = auth.Store()
		handler.Orchestrator = repackage.NewOrchestrator(auth.Store(), auth.DB())
		handler.GitHubWebhookSecret = auth.GitHubWebhookSecret()
	})
}

type route struct {
	project string
	version string
	os      string
	arch    string
	write   bool
}

func (r route) ProjectName() string { return r.project }
func (r route) Access() auth.AccessLevel {
	if r.write {
		return auth.WriteAccess
	}
	return auth.ReadAccess
}

func parseRoute(r *http.Request) auth.RouteInfo {
	return route{
		project: r.PathValue("project"),
		version: r.PathValue("version"),
		os:      r.PathValue("os"),
		arch:    r.PathValue("arch"),
		write:   r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE",
	}
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

type Handler struct {
	DB                  *db.DB
	Store               storage.Storage
	Orchestrator        *repackage.Orchestrator
	GitHubWebhookSecret string
}

const maxJSONBody = 1 << 20 // 1 MiB

func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	jsonResponse(w, status, map[string]string{"error": msg})
}

func (h *Handler) requireWrite(w http.ResponseWriter, r *http.Request) *db.APIToken {
	t := auth.TokenFrom(r.Context())
	if t == nil || !t.HasScope("write") {
		jsonError(w, http.StatusUnauthorized, "authentication required")
		return nil
	}
	return t
}

func (h *Handler) requireGlobalWrite(w http.ResponseWriter, r *http.Request) *db.APIToken {
	t := auth.TokenFrom(r.Context())
	if t == nil || !t.HasScope("write") || !t.IsGlobal() {
		jsonError(w, http.StatusForbidden, "global write token required")
		return nil
	}
	return t
}

func (h *Handler) getRelease(w http.ResponseWriter, r *http.Request, projectID int64, version string) *db.Release {
	rel, err := h.DB.GetRelease(r.Context(), projectID, version)
	if err != nil {
		if err == db.ErrNotFound {
			jsonError(w, http.StatusNotFound, "release not found")
		} else {
			jsonError(w, http.StatusInternalServerError, "failed to get release")
		}
		return nil
	}
	return rel
}

// getLatestRelease resolves the apex "latest" release (newest published release
// on the default branch, falling back to newest published overall) the same way
// dl/static/web do. It writes the error response and returns nil on failure.
func (h *Handler) getLatestRelease(w http.ResponseWriter, r *http.Request, projectID int64) *db.Release {
	rel, err := h.DB.GetLatestRelease(r.Context(), projectID)
	if err != nil {
		if err == db.ErrNotFound {
			jsonError(w, http.StatusNotFound, "release not found")
		} else {
			jsonError(w, http.StatusInternalServerError, "failed to get release")
		}
		return nil
	}
	return rel
}

func validateScopes(w http.ResponseWriter, scopes string) string {
	if scopes == "" {
		return "read"
	}
	var parts []string
	for _, s := range strings.Split(scopes, ",") {
		s = strings.TrimSpace(s)
		if !db.ValidScopes[s] {
			jsonError(w, http.StatusBadRequest, "invalid scope: "+s)
			return ""
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ",")
}
