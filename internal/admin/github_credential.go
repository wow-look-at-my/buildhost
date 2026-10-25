// The GitHub credential page: set or clear the token buildhost authenticates
// its own GitHub calls with. The token never leaves the server.

package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

const maxCredentialBody = 4 << 10

var credentialHTTPClient = &http.Client{Timeout: 15 * time.Second}

// verifyGitHubToken asks GitHub, through the mirror, whose token this is.
var verifyGitHubToken = func(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, auth.GitHubAPI+"/user", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "buildhost")
	resp, err := credentialHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("GitHub answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&user); err != nil {
		return "", fmt.Errorf("decode GitHub user: %w", err)
	}
	return user.Login, nil
}

// apiGitHubCredential (GET /api/github-credential) says which credential is in
// use and where it came from. It never returns the token.
func (s *Server) apiGitHubCredential(w http.ResponseWriter, r *http.Request) {
	_, updatedAt, found, err := s.db.GitHubCredential(r.Context())
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp := map[string]any{
		"source":        auth.GitHubCredentialSource(),
		"env_token":     s.cfg.GitHubToken != "",
		"dashboard_set": found,
		"github_api":    auth.GitHubAPI,
	}
	if found {
		resp["dashboard_updated_at"] = updatedAt
	}
	s.writeJSON(w, resp)
}

// apiSetGitHubCredential (PUT /api/github-credential) checks a token with
// GitHub, stores it and puts it in use at once. Body {token}.
func (s *Server) apiSetGitHubCredential(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GitHubToken != "" {
		http.Error(w, "BUILDHOST_GITHUB_TOKEN is set in the environment and takes precedence; remove it to manage the token here", http.StatusConflict)
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCredentialBody)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	token := strings.TrimSpace(body.Token)
	if token == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}
	login, err := verifyGitHubToken(r.Context(), token)
	if err != nil {
		http.Error(w, "GitHub refused the token: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.db.SetGitHubCredential(r.Context(), token); err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	auth.SetGitHubToken(token)
	slog.Info("github credential set from the admin dashboard", "login", login)
	s.writeJSON(w, map[string]any{"login": login, "source": auth.GitHubCredentialSource()})
}

// apiDeleteGitHubCredential (DELETE /api/github-credential) removes the stored
// token and stops using it.
func (s *Server) apiDeleteGitHubCredential(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GitHubToken != "" {
		http.Error(w, "BUILDHOST_GITHUB_TOKEN is set in the environment; remove it there", http.StatusConflict)
		return
	}
	if err := s.db.DeleteGitHubCredential(r.Context()); err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	auth.SetGitHubToken("")
	s.writeJSON(w, map[string]any{"source": auth.GitHubCredentialSource()})
}
