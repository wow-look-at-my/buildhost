// The GitHub page: which credential buildhost's own GitHub calls use, and
// what the last secret-server fetch did. It never shows a secret.

package admin

import (
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/secretserver"
)

// configProblems lists the settings that are set but cannot work. Every page shows them in a banner.
func (s *Server) configProblems() []string {
	problems := []string{}
	if p := s.conf().GitHubAppProblem(); p != "" {
		problems = append(problems, p)
	}
	return problems
}

// apiGitHubCredential (GET /api/github-credential).
func (s *Server) apiGitHubCredential(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, map[string]any{
		"source":         auth.GitHubCredentialSource(),
		"env_credential": s.conf().GitHubToken != "" || s.conf().HasGitHubApp(),
		"secret_server":  secretserver.LastStatus(),
		"github_api":     auth.GitHubAPI,
		"token_set":      s.conf().GitHubToken != "",
		"app_id_set":     s.conf().GitHubAppID != "",
		"app_key_set":    s.conf().GitHubAppPrivateKey != "",
		"app_problem":    s.conf().GitHubAppProblem(),
		"app_error":      auth.GitHubAppLastError(),
	})
}
