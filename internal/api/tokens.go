package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

type createTokenRequest struct {
	Name      string `json:"name"`
	ProjectID *int64 `json:"project_id"`
	Scopes    string `json:"scopes"`
}

func (h *Handler) CreateToken(w http.ResponseWriter, r *http.Request) {
	t := h.requireWrite(w, r)
	if t == nil {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	var req createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		jsonError(w, http.StatusBadRequest, "name is required")
		return
	}

	scopes := validateScopes(w, req.Scopes)
	if scopes == "" {
		return
	}

	// New tokens may only grant scopes the caller already holds.
	for _, s := range strings.Split(scopes, ",") {
		if !t.HasScope(s) {
			jsonError(w, http.StatusForbidden, "cannot grant scope not held by caller: "+s)
			return
		}
	}

	if !t.IsGlobal() {
		if req.ProjectID == nil || *req.ProjectID != *t.ProjectID {
			jsonError(w, http.StatusForbidden, "project-scoped tokens can only create tokens for the same project")
			return
		}
	}

	plaintext, token, err := h.DB.CreateToken(r.Context(), req.Name, req.ProjectID, scopes)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	jsonResponse(w, http.StatusCreated, map[string]any{
		"token":   plaintext,
		"details": token,
	})
}

func (h *Handler) ListTokens(w http.ResponseWriter, r *http.Request) {
	t := h.requireWrite(w, r)
	if t == nil {
		return
	}

	if !t.IsGlobal() {
		jsonError(w, http.StatusForbidden, "only global tokens can list tokens")
		return
	}

	tokens, err := h.DB.ListTokens(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to list tokens")
		return
	}

	jsonResponse(w, http.StatusOK, tokens)
}

func (h *Handler) DeleteToken(w http.ResponseWriter, r *http.Request) {
	t := h.requireWrite(w, r)
	if t == nil {
		return
	}

	if !t.IsGlobal() {
		jsonError(w, http.StatusForbidden, "only global tokens can delete tokens")
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid token id")
		return
	}

	if id == t.ID {
		jsonError(w, http.StatusBadRequest, "cannot delete your own token")
		return
	}

	if err := h.DB.DeleteToken(r.Context(), id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "token not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to delete token")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
