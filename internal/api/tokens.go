package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

type createTokenRequest struct {
	Name      string `json:"name"`
	ProjectID *int64 `json:"project_id"`
	Scopes    string `json:"scopes"`
}

func (h *Handler) CreateToken(w http.ResponseWriter, r *http.Request) {
	t := auth.TokenFrom(r.Context())
	if t == nil || !t.HasScope("write") {
		jsonError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		jsonError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Scopes == "" {
		req.Scopes = "read,write"
	}

	plaintext, token, err := h.DB.CreateToken(r.Context(), req.Name, req.ProjectID, req.Scopes)
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
	t := auth.TokenFrom(r.Context())
	if t == nil || !t.HasScope("write") {
		jsonError(w, http.StatusUnauthorized, "authentication required")
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
	t := auth.TokenFrom(r.Context())
	if t == nil || !t.HasScope("write") {
		jsonError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid token id")
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
