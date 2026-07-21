package admin

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

type tokenResp struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"`
	IsGlobal    bool       `json:"is_global"`
	ProjectID   *int64     `json:"project_id,omitempty"`
	ProjectName string     `json:"project_name"`
	Scopes      string     `json:"scopes"`
	IsExpired   bool       `json:"is_expired"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

func (s *Server) apiTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.db.ListTokenDetails(r.Context())
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := make([]tokenResp, 0, len(tokens))
	for _, t := range tokens {
		resp = append(resp, tokenResp{
			ID:          t.ID,
			Name:        t.Name,
			TokenPrefix: t.TokenPrefix,
			IsGlobal:    t.IsGlobal(),
			ProjectID:   t.ProjectID,
			ProjectName: t.ProjectName,
			Scopes:      t.Scopes,
			IsExpired:   t.IsExpired(),
			CreatedAt:   t.CreatedAt,
			LastUsedAt:  t.LastUsedAt,
			ExpiresAt:   t.ExpiresAt,
		})
	}
	s.writeJSON(w, resp)
}

func (s *Server) apiCreateToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		Scopes    string `json:"scopes"`
		ProjectID *int64 `json:"project_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	scopes := req.Scopes
	if scopes == "" {
		scopes = "read"
	}

	plaintext, token, err := s.db.CreateToken(r.Context(), req.Name, req.ProjectID, scopes)
	if err != nil {
		slog.Error("admin create token", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]any{
		"token": plaintext,
		"details": tokenResp{
			ID:          token.ID,
			Name:        token.Name,
			TokenPrefix: token.TokenPrefix,
			IsGlobal:    token.IsGlobal(),
			ProjectID:   token.ProjectID,
			Scopes:      token.Scopes,
		},
	})
}

func (s *Server) apiUpdateToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var req struct {
		Name   string `json:"name"`
		Scopes string `json:"scopes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	if req.Scopes == "" {
		http.Error(w, "scopes required", http.StatusBadRequest)
		return
	}
	if err := s.db.UpdateToken(r.Context(), id, req.Name, req.Scopes); err != nil {
		slog.Error("admin update token", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiDeleteToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := s.db.DeleteToken(r.Context(), id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("admin delete token", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
