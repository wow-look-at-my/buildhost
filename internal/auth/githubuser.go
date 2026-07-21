package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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
	// incorrect_client_credentials, redirect_uri_mismatch, ...) as an error JSON
	// body, typically under HTTP 200 -- so classify by body, not status, and
	// carry GitHub's own diagnosis in the error (it names no secrets or codes).
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
// 200. Results are cached per (login, repo, token) for a short TTL so the GitHub
// call does not run on every asset request.
//
// Two properties keep the cache from ever locking out a user who DOES have
// access (the failure that made an authorized repo owner see "Access denied"):
//
//   - Only an *authoritative* answer is cached -- a definite 200 (access) or 404
//     (no access / not visible to this token). A transient failure (network
//     error, 5xx, 429, or a 401/403 that is typically a rate limit or a token
//     still propagating) denies only the current request and is NOT cached, so a
//     refresh re-checks immediately instead of being pinned for the whole TTL on
//     one momentary GitHub hiccup.
//   - The cache key includes a fingerprint of the token, so a user who re-signs
//     in with a fresh, broader-scoped token is never shadowed by a negative
//     cached against their previous token.
func (g *GitHubAuth) canAccessRepo(ctx context.Context, login, token, ownerRepo string) bool {
	if login == "" || token == "" || !validRepoPath(ownerRepo) {
		return false
	}
	key := login + "\x00" + ownerRepo + "\x00" + tokenFingerprint(token)
	now := time.Now()
	g.mu.Lock()
	if e, ok := g.repoCache[key]; ok && now.Before(e.exp) {
		g.mu.Unlock()
		return e.allowed
	}
	g.mu.Unlock()

	allowed, authoritative := g.checkRepoAccess(ctx, token, ownerRepo)
	if !authoritative {
		// Non-answer (transient error / rate limit): fail closed for this request
		// but do not cache, so the next request re-checks rather than inheriting a
		// stale denial.
		return allowed
	}

	g.mu.Lock()
	g.repoCache[key] = repoAccess{allowed: allowed, exp: now.Add(repoAccessTTL)}
	g.mu.Unlock()
	return allowed
}

// checkRepoAccess performs GET /repos/{owner}/{repo} and classifies the result.
// allowed is true only on 200. authoritative is true only when GitHub gave a
// definite answer -- a 200 (access) or a 404 (no access; GitHub 404s a repo the
// token cannot see, rather than 403, so existence never leaks). Anything else
// (network error, 5xx, 429, or a 401/403 that is usually a rate limit or a
// freshly minted token still propagating) is treated as non-authoritative so the
// caller never caches a non-answer as a hard denial.
func (g *GitHubAuth) checkRepoAccess(ctx context.Context, token, ownerRepo string) (allowed, authoritative bool) {
	req, err := http.NewRequestWithContext(ctx, "GET", githubAPIBase+"/repos/"+ownerRepo, nil)
	if err != nil {
		return false, false
	}
	setGitHubHeaders(req, token)
	resp, err := g.http.Do(req)
	if err != nil {
		return false, false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	switch resp.StatusCode {
	case http.StatusOK:
		return true, true
	case http.StatusNotFound:
		return false, true
	default:
		return false, false
	}
}

// tokenFingerprint returns a short, non-reversible fingerprint of a token, used
// only as part of the in-memory repo-access cache key so the raw token is never
// held as a map key while two distinct tokens still hash to distinct keys.
func tokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

func setGitHubHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "buildhost")
}
