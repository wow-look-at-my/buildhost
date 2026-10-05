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
// ownerRepo. Only a definite answer is cached. A failed lookup denies. The
// reason says why a denial happened, for the caller's error.
func gitHubUserReadsRepo(ctx context.Context, login, ownerRepo string) (bool, string) {
	if !validGitHubSegment(login) || !validRepoPath(ownerRepo) {
		return false, fmt.Sprintf("actor %q or repo %q is not a valid GitHub name", login, ownerRepo)
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
	} else {
		slog.ErrorContext(ctx, "github permission check could not answer, denying", "repo", ownerRepo, "user", login, "reason", reason)
	}
	return allowed, reason
}

func fetchUserRepoPermission(ctx context.Context, login, ownerRepo string) (allowed, definite bool, reason string) {
	owner, repo, _ := strings.Cut(ownerRepo, "/")
	bearer := bearerForRepo(ctx, owner, repo)
	if bearer == "" {
		return false, false, "buildhost has no GitHub credential for " + ownerRepo
	}

	ctx, cancel := context.WithTimeout(ctx, branchLookupBudget)
	defer cancel()
	u := gitHubAPIBase + "/repos/" + ownerRepo + "/collaborators/" + url.PathEscape(login) + "/permission"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, false, "cannot build the GitHub request: " + err.Error()
	}
	setGitHubHeaders(req, bearer)
	resp, err := githubBranchHTTPClient.Do(req)
	if err != nil {
		return false, false, "GitHub permission lookup failed: " + err.Error()
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return false, false, "buildhost's GitHub credential cannot see " + ownerRepo + " (HTTP 404)"
	default:
		return false, false, fmt.Sprintf("GitHub permission lookup answered HTTP %d", resp.StatusCode)
	}
	var body struct {
		Permission string `json:"permission"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body); err != nil {
		return false, false, "cannot read GitHub's permission answer: " + err.Error()
	}
	switch body.Permission {
	case "admin", "maintain", "write", "triage", "read":
		return true, true, ""
	}
	return false, true, fmt.Sprintf("GitHub says %s has %q permission on %s", login, body.Permission, ownerRepo)
}
