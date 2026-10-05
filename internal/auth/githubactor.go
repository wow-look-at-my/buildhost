package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type actorReadEntry struct {
	allowed bool
	reason  string
	exp     time.Time
}

var (
	actorReadMu    sync.Mutex
	actorReadCache = map[string]actorReadEntry{}
)

// gitHubUserReadsRepo asks GitHub whether login has read access or higher on
// ownerRepo. On a refusal, reason says why without the repo name. Only a
// definite answer is cached. A failed lookup denies.
func gitHubUserReadsRepo(ctx context.Context, login, ownerRepo string) (allowed bool, reason string) {
	if login == "" {
		return false, "the token names no GitHub actor"
	}
	if !validGitHubSegment(login) {
		return false, fmt.Sprintf("GitHub actor %q is not a user login", login)
	}
	if !validRepoPath(ownerRepo) {
		return false, "the project records no GitHub repo"
	}
	key := strings.ToLower(login + "\x00" + ownerRepo)
	now := time.Now()
	actorReadMu.Lock()
	if e, ok := actorReadCache[key]; ok && now.Before(e.exp) {
		actorReadMu.Unlock()
		return e.allowed, e.reason
	}
	actorReadMu.Unlock()

	allowed, definite, reason := fetchUserRepoPermission(ctx, login, ownerRepo)
	if definite {
		actorReadMu.Lock()
		actorReadCache[key] = actorReadEntry{allowed: allowed, reason: reason, exp: now.Add(repoAccessTTL)}
		actorReadMu.Unlock()
	}
	return allowed, reason
}

func fetchUserRepoPermission(ctx context.Context, login, ownerRepo string) (allowed, definite bool, reason string) {
	owner, repo, _ := strings.Cut(ownerRepo, "/")
	bearer := bearerForRepo(ctx, owner, repo)
	if bearer == "" {
		slog.WarnContext(ctx, "github permission check: no credential for repo", "repo", ownerRepo)
		return false, false, "buildhost holds no GitHub credential for the project's repo"
	}

	ctx, cancel := context.WithTimeout(ctx, branchLookupBudget)
	defer cancel()
	u := gitHubAPIBase + "/repos/" + ownerRepo + "/collaborators/" + url.PathEscape(login) + "/permission"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, false, "the GitHub permission lookup could not be built"
	}
	setGitHubHeaders(req, bearer)
	resp, err := githubBranchHTTPClient.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "github permission check failed", "repo", ownerRepo, "user", login, "err", err)
		return false, false, "the GitHub permission lookup failed to connect"
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		slog.WarnContext(ctx, "github permission check: 404", "repo", ownerRepo, "user", login)
		return false, true, fmt.Sprintf("GitHub answered 404 for the permission of %s on the project's repo", login)
	default:
		slog.WarnContext(ctx, "github permission check: transient failure, denying without caching", "repo", ownerRepo, "user", login, "status", resp.StatusCode)
		return false, false, fmt.Sprintf("GitHub answered HTTP %d for the permission of %s on the project's repo", resp.StatusCode, login)
	}
	var body struct {
		Permission string `json:"permission"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body); err != nil {
		return false, false, "GitHub sent a permission response that does not parse"
	}
	switch body.Permission {
	case "admin", "maintain", "write", "triage", "read":
		return true, true, ""
	}
	return false, true, fmt.Sprintf("GitHub reports permission %q for %s on the project's repo", body.Permission, login)
}
