package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// --- GitHub API ---

// exchangeCode trades an authorization code for a user access token.
func (g *GitHubAuth) exchangeCode(ctx context.Context, code, redirectURI string) (string, error) {
	if code == "" {
		return "", fmt.Errorf("missing code")
	}
	form := url.Values{
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", githubTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := g.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	// GitHub reports exchange failures (bad_verification_code,
	var body struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("token endpoint HTTP %d: %w", resp.StatusCode, err)
	}
	if body.AccessToken == "" {
		if body.Error == "" {
			return "", fmt.Errorf("token endpoint HTTP %d: no access token in response", resp.StatusCode)
		}
		return "", fmt.Errorf("token endpoint HTTP %d: %s: %s", resp.StatusCode, body.Error, body.ErrorDesc)
	}
	return body.AccessToken, nil
}

// fetchLogin returns the authenticated user's GitHub login (GET /user).
func (g *GitHubAuth) fetchLogin(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", githubAPIBase+"/user", nil)
	if err != nil {
		return "", err
	}
	setGitHubHeaders(req, token)
	resp, err := g.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET /user -> %d", resp.StatusCode)
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&user); err != nil || user.Login == "" {
		return "", fmt.Errorf("no login in /user response")
	}
	return user.Login, nil
}

// canAccessRepo reports whether the signed-in user (identified by login, using
// their token) can access ownerRepo -- i.e. GET /repos/{owner}/{repo} returns
func (g *GitHubAuth) canAccessRepo(ctx context.Context, login, token, ownerRepo string) (allowed, tokenDead bool) {
	if login == "" || token == "" || !validRepoPath(ownerRepo) {
		return false, false
	}
	key := login + "\x00" + ownerRepo + "\x00" + tokenFingerprint(token)
	now := time.Now()
	g.mu.Lock()
	if e, ok := g.repoCache[key]; ok && now.Before(e.exp) {
		g.mu.Unlock()
		return e.result == repoCheckAllowed, e.result == repoCheckTokenDead
	}
	g.mu.Unlock()

	result := g.checkRepoAccess(ctx, token, ownerRepo)
	if result == repoCheckTransient {
		// Non-answer (transient error / rate limit): fail closed for this request
		return false, false
	}

	g.mu.Lock()
	g.repoCache[key] = repoAccess{result: result, exp: now.Add(repoAccessTTL)}
	g.mu.Unlock()
	return result == repoCheckAllowed, result == repoCheckTokenDead
}

// checkRepoAccess performs GET /repos/{owner}/{repo} and classifies the result
func (g *GitHubAuth) checkRepoAccess(ctx context.Context, token, ownerRepo string) repoCheckResult {
	req, err := http.NewRequestWithContext(ctx, "GET", githubAPIBase+"/repos/"+ownerRepo, nil)
	if err != nil {
		return repoCheckTransient
	}
	setGitHubHeaders(req, token)
	resp, err := g.http.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "github repo-access check failed", "repo", ownerRepo, "err", err)
		return repoCheckTransient
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	switch resp.StatusCode {
	case http.StatusOK:
		return repoCheckAllowed
	case http.StatusNotFound:
		return repoCheckNoAccess
	case http.StatusUnauthorized:
		slog.WarnContext(ctx, "github repo-access check: token rejected, session token is dead", "repo", ownerRepo, "status", resp.StatusCode)
		return repoCheckTokenDead
	default:
		slog.WarnContext(ctx, "github repo-access check: transient failure, denying without caching", "repo", ownerRepo, "status", resp.StatusCode)
		return repoCheckTransient
	}
}

// tokenFingerprint returns a short, non-reversible fingerprint of a token, used
func tokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

func setGitHubHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "buildhost")
}
