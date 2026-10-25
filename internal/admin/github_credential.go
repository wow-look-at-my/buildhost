// The GitHub page: which credential buildhost's own GitHub calls use, and
// what the last secret-server fetch did. It never shows a secret.

package admin

import (
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/secretserver"
)

// apiGitHubCredential (GET /api/github-credential).
func (s *Server) apiGitHubCredential(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, map[string]any{
		"source":         auth.GitHubCredentialSource(),
		"env_credential": s.cfg.GitHubToken != "" || s.cfg.GitHubAppID != "",
		"secret_server":  secretserver.LastStatus(),
		"github_api":     auth.GitHubAPI,
	})
}
