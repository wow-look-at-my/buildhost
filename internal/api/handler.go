package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

type Handler struct {
	DB           *db.DB
	Store        storage.Storage
	Orchestrator *repackage.Orchestrator
}

func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	jsonResponse(w, status, map[string]string{"error": msg})
}

// requireWrite checks that the request has a write-scoped token, writing a 401 on failure.
func (h *Handler) requireWrite(w http.ResponseWriter, r *http.Request) *model.APIToken {
	t := auth.TokenFrom(r.Context())
	if t == nil || !t.HasScope("write") {
		jsonError(w, http.StatusUnauthorized, "authentication required")
		return nil
	}
	return t
}

// requireGlobalWrite checks for a global write-scoped token, writing a 403 on failure.
func (h *Handler) requireGlobalWrite(w http.ResponseWriter, r *http.Request) *model.APIToken {
	t := auth.TokenFrom(r.Context())
	if t == nil || !t.HasScope("write") || !t.IsGlobal() {
		jsonError(w, http.StatusForbidden, "global write token required")
		return nil
	}
	return t
}

// getProject fetches a project by name, writing the error response and returning nil on failure.
func (h *Handler) getProject(w http.ResponseWriter, r *http.Request, name string) *model.Project {
	p, err := h.DB.GetProject(r.Context(), name)
	if errors.Is(err, db.ErrNotFound) {
		jsonError(w, http.StatusNotFound, "project not found")
		return nil
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to get project")
		return nil
	}
	return p
}

// checkReadAccess verifies the caller can read a private project, writing a 401 on failure.
func (h *Handler) checkReadAccess(w http.ResponseWriter, r *http.Request, project *model.Project) bool {
	if project.IsPrivate {
		t := auth.TokenFrom(r.Context())
		if t == nil || !t.HasScope("read") {
			jsonError(w, http.StatusUnauthorized, "authentication required")
			return false
		}
	}
	return true
}

// getRelease fetches a release by project ID and version, writing the error response and returning nil on failure.
func (h *Handler) getRelease(w http.ResponseWriter, r *http.Request, projectID int64, version string) *model.Release {
	rel, err := h.DB.GetRelease(r.Context(), projectID, version)
	if errors.Is(err, db.ErrNotFound) {
		jsonError(w, http.StatusNotFound, "release not found")
		return nil
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to get release")
		return nil
	}
	return rel
}

// validateScopes normalizes and validates a comma-separated scope string.
// An empty input defaults to "read,write". Returns "" and writes a 400 on invalid scope.
func validateScopes(w http.ResponseWriter, scopes string) string {
	if scopes == "" {
		return "read,write"
	}
	var parts []string
	for _, s := range strings.Split(scopes, ",") {
		s = strings.TrimSpace(s)
		if !model.ValidScopes[s] {
			jsonError(w, http.StatusBadRequest, "invalid scope: "+s)
			return ""
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ",")
}
