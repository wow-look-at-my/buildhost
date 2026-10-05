package auth

import (
	"context"
	"encoding/json"
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
	exp     time.Time
}

var (
	actorReadMu    sync.Mutex
	actorReadCache = map[string]actorReadEntry{}
)

// gitHubUserReadsRepo asks GitHub whether login has read access or higher on
// ownerRepo. Only a definite answer is cached. A failed lookup denies.
func gitHubUserReadsRepo(ctx context.Context, login, ownerRepo string) bool {
	if !validGitHubSegment(login) || !validRepoPath(ownerRepo) {
		return false
	}
	key := strings.ToLower(login + "\x00" + ownerRepo)
	now := time.Now()
	actorReadMu.Lock()
	if e, ok := actorReadCache[key]; ok && now.Before(e.exp) {
		actorReadMu.Unlock()
		return e.allowed
	}
	actorReadMu.Unlock()

	allowed, definite := fetchUserRepoPermission(ctx, login, ownerRepo)
	if definite {
		actorReadMu.Lock()
		actorReadCache[key] = actorReadEntry{allowed: allowed, exp: now.Add(repoAccessTTL)}
		actorReadMu.Unlock()
	}
	return allowed
}

func fetchUserRepoPermission(ctx context.Context, login, ownerRepo string) (allowed, definite bool) {
	owner, repo, _ := strings.Cut(ownerRepo, "/")
	bearer := bearerForRepo(ctx, owner, repo)
	if bearer == "" {
		slog.WarnContext(ctx, "github permission check: no credential for repo", "repo", ownerRepo)
		return false, false
	}

	ctx, cancel := context.WithTimeout(ctx, branchLookupBudget)
	defer cancel()
	u := gitHubAPIBase + "/repos/" + ownerRepo + "/collaborators/" + url.PathEscape(login) + "/permission"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, false
	}
	setGitHubHeaders(req, bearer)
	resp, err := githubBranchHTTPClient.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "github permission check failed", "repo", ownerRepo, "user", login, "err", err)
		return false, false
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return false, true
	default:
		slog.WarnContext(ctx, "github permission check: transient failure, denying without caching", "repo", ownerRepo, "user", login, "status", resp.StatusCode)
		return false, false
	}
	var body struct {
		Permission string `json:"permission"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body); err != nil {
		return false, false
	}
	switch body.Permission {
	case "admin", "maintain", "write", "triage", "read":
		return true, true
	}
	return false, true
}
