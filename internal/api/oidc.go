package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func init() {
	auth.HandleRaw("POST /api/v1/oidc/policies", handler.CreateOIDCPolicy)
	auth.HandleRaw("GET /api/v1/oidc/policies", handler.ListOIDCPolicies)
	auth.HandleRaw("DELETE /api/v1/oidc/policies/{id}", handler.DeleteOIDCPolicy)
}

type createOIDCPolicyRequest struct {
	Issuer         string `json:"issuer"`
	SubjectPattern string `json:"subject_pattern"`
	Audience       string `json:"audience"`
	ProjectID      *int64 `json:"project_id"`
	Scopes         string `json:"scopes"`
}

func (h *Handler) CreateOIDCPolicy(w http.ResponseWriter, r *http.Request) {
	if h.requireGlobalWrite(w, r) == nil {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	var req createOIDCPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Issuer == "" || req.SubjectPattern == "" {
		jsonError(w, http.StatusBadRequest, "issuer and subject_pattern are required")
		return
	}

	scopes := validateScopes(w, req.Scopes)
	if scopes == "" {
		return
	}

	p := &db.OIDCPolicy{
		Issuer:         req.Issuer,
		SubjectPattern: req.SubjectPattern,
		Audience:       req.Audience,
		ProjectID:      req.ProjectID,
		Scopes:         scopes,
	}

	if err := h.DB.CreateOIDCPolicy(r.Context(), p); err != nil {
		if errors.Is(err, db.ErrConflict) {
			jsonError(w, http.StatusConflict, "policy already exists for this issuer/subject")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to create policy")
		return
	}

	jsonResponse(w, http.StatusCreated, p)
}

func (h *Handler) ListOIDCPolicies(w http.ResponseWriter, r *http.Request) {
	if h.requireGlobalWrite(w, r) == nil {
		return
	}

	policies, err := h.DB.ListOIDCPolicies(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to list policies")
		return
	}
	if policies == nil {
		policies = []db.OIDCPolicy{}
	}

	jsonResponse(w, http.StatusOK, policies)
}

func (h *Handler) DeleteOIDCPolicy(w http.ResponseWriter, r *http.Request) {
	if h.requireGlobalWrite(w, r) == nil {
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid policy id")
		return
	}

	if err := h.DB.DeleteOIDCPolicy(r.Context(), id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "policy not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to delete policy")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
