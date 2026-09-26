package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// A run lock holds the first value a CI run records under a name, so every
// job of that run reads the same value.
func init() {
	auth.HandleRawPrimary("GET /api/v1/run-locks", handler.GetRunLock)
	auth.HandleRawPrimary("POST /api/v1/run-locks", handler.ClaimRunLock)
}

// runLockRequest names the run the caller believes it is in. The token decides
// the run; a request that names another run is refused rather than redirected.
type runLockRequest struct {
	Repository string `json:"repository"`
	RunID      string `json:"run_id"`
	RunAttempt string `json:"run_attempt"`
	Name       string `json:"name"`
	Value      string `json:"value"`
}

type runLockResponse struct {
	Found   bool   `json:"found"`
	Value   string `json:"value"`
	Created bool   `json:"created"`
}

// maxRunLockField bounds a name or a value.
const maxRunLockField = 1024

// runLockKey returns the key for req, from the run the request's OIDC token
// names. It writes the error and returns false when there is none.
func runLockKey(w http.ResponseWriter, r *http.Request, req runLockRequest) (db.RunLockKey, bool) {
	t := auth.TokenFrom(r.Context())
	if t == nil {
		jsonError(w, http.StatusUnauthorized, "authentication required: present the job's GitHub Actions OIDC token")
		return db.RunLockKey{}, false
	}
	repo := auth.OIDCRepoFrom(r.Context())
	if repo.RepoID == "" || repo.RunID == "" || repo.RunAttempt == "" {
		jsonError(w, http.StatusForbidden, "a run lock needs a GitHub Actions OIDC token that names its repository id, run_id and run_attempt")
		return db.RunLockKey{}, false
	}
	if !strings.EqualFold(req.Repository, repo.RepoPath) || req.RunID != repo.RunID || req.RunAttempt != repo.RunAttempt {
		jsonError(w, http.StatusForbidden, "the request names run "+req.Repository+"#"+req.RunID+"."+req.RunAttempt+", but the token was minted by run "+repo.RepoPath+"#"+repo.RunID+"."+repo.RunAttempt)
		return db.RunLockKey{}, false
	}
	if req.Name == "" || len(req.Name) > maxRunLockField {
		jsonError(w, http.StatusBadRequest, "name is required and must be at most 1024 bytes")
		return db.RunLockKey{}, false
	}
	return db.RunLockKey{RepoID: repo.RepoID, RunID: repo.RunID, RunAttempt: repo.RunAttempt, Name: req.Name}, true
}

// GetRunLock answers whether the caller's run holds a lock under a name.
func (h *Handler) GetRunLock(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	key, ok := runLockKey(w, r, runLockRequest{
		Repository: q.Get("repository"),
		RunID:      q.Get("run_id"),
		RunAttempt: q.Get("run_attempt"),
		Name:       q.Get("name"),
	})
	if !ok {
		return
	}
	value, found, err := h.DB.GetRunLock(r.Context(), key)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to read run lock")
		return
	}
	jsonResponse(w, http.StatusOK, runLockResponse{Found: found, Value: value})
}

// ClaimRunLock records a value under a name for the caller's run, unless the run
// holds one there already. It answers with the value the run now holds.
func (h *Handler) ClaimRunLock(w http.ResponseWriter, r *http.Request) {
	t := h.requireWrite(w, r)
	if t == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	var req runLockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	key, ok := runLockKey(w, r, req)
	if !ok {
		return
	}
	if req.Value == "" || len(req.Value) > maxRunLockField {
		jsonError(w, http.StatusBadRequest, "value is required and must be at most 1024 bytes")
		return
	}
	value, created, err := h.DB.ClaimRunLock(r.Context(), key, req.Value, time.Now())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to claim run lock")
		return
	}
	jsonResponse(w, http.StatusOK, runLockResponse{Found: true, Value: value, Created: created})
}
