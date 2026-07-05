package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// GitHubActionsIssuer is the canonical GitHub Actions OIDC issuer. Default-branch
// resolution is gated on it: only a token minted by GitHub Actions carries a
// `repo:OWNER/REPO:...` subject that maps to a github.com repository, so only
// then is a GitHub REST lookup meaningful. Other OIDC providers are left alone.
const GitHubActionsIssuer = "https://token.actions.githubusercontent.com"

// gitHubAPIBase is the GitHub REST base. A var (not const) so tests can point it
// at a stub server instead of reaching the real api.github.com.
var gitHubAPIBase = "https://api.github.com"

// githubToken, when set (BUILDHOST_GITHUB_TOKEN), authenticates default-branch
// lookups. Anonymous github.com is throttled to 60 requests/hour/IP, which a
// shared egress can exhaust; an authenticated token raises that to 5000/hour.
// Resolution still works anonymously (best-effort) when no token is configured.
var (
	githubToken   string
	githubTokenMu sync.RWMutex
)

// SetGitHubToken configures the token used for default-branch lookups. Called
// once at startup from config; empty means anonymous (best-effort) lookups.
func SetGitHubToken(t string) {
	githubTokenMu.Lock()
	githubToken = t
	githubTokenMu.Unlock()
}

func currentGitHubToken() string {
	githubTokenMu.RLock()
	defer githubTokenMu.RUnlock()
	return githubToken
}

var githubBranchHTTPClient = &http.Client{Timeout: 5 * time.Second}

const (
	branchPositiveTTL  = time.Hour       // cache a resolved branch for an hour
	branchNegativeTTL  = 5 * time.Minute // back off briefly on failure (rate limit, outage)
	branchLookupBudget = 4 * time.Second
)

type branchCacheEntry struct {
	branch string
	expiry time.Time
}

var (
	branchCacheMu sync.Mutex
	branchCache   = map[string]branchCacheEntry{}
)

// GitHubDefaultBranch returns the default branch GitHub reports for "owner/repo",
// resolved from the REST API and cached. It is best-effort: it returns "" when
// the branch cannot be determined (rate limit, private repo without a token,
// network error, malformed input). The caller decides what to do with "" (keep
// the project's existing default branch). Callers must gate on the GitHub
// Actions issuer -- this reaches github.com regardless of the OIDC provider.
func GitHubDefaultBranch(ctx context.Context, repoPath string) string {
	if !validRepoPath(repoPath) {
		return ""
	}

	now := time.Now()
	branchCacheMu.Lock()
	if e, ok := branchCache[repoPath]; ok && now.Before(e.expiry) {
		branch := e.branch
		branchCacheMu.Unlock()
		return branch
	}
	branchCacheMu.Unlock()

	branch := fetchGitHubDefaultBranch(ctx, repoPath)

	ttl := branchPositiveTTL
	if branch == "" {
		ttl = branchNegativeTTL
	}
	branchCacheMu.Lock()
	branchCache[repoPath] = branchCacheEntry{branch: branch, expiry: time.Now().Add(ttl)}
	branchCacheMu.Unlock()
	return branch
}

func fetchGitHubDefaultBranch(ctx context.Context, repoPath string) string {
	// Obtain the bearer first (a GitHub App installation token, a static PAT, or
	// none) -- its own lookups are separately bounded, so they don't eat into the
	// repos call's budget below.
	owner, repo, _ := strings.Cut(repoPath, "/")
	bearer := bearerForRepo(ctx, owner, repo)

	// Bound the lookup so a slow github.com never stalls a publish for the full
	// client timeout, and never outlives the request.
	ctx, cancel := context.WithTimeout(ctx, branchLookupBudget)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gitHubAPIBase+"/repos/"+repoPath, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "buildhost")
	req.Header.Set("Accept", "application/vnd.github+json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := githubBranchHTTPClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var body struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return ""
	}
	if !validRefName(body.DefaultBranch) {
		return ""
	}
	return body.DefaultBranch
}

// validRepoPath reports whether s is a safe "owner/repo" to interpolate into a
// GitHub REST URL: exactly one slash, each segment a conservative subset of the
// characters GitHub allows in owner/repo names.
func validRepoPath(s string) bool {
	owner, repo, ok := strings.Cut(s, "/")
	if !ok || strings.Contains(repo, "/") {
		return false
	}
	return validGitHubSegment(owner) && validGitHubSegment(repo)
}

func validGitHubSegment(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// validRefName sanity-checks a branch name returned by GitHub before it is
// trusted as a project's default branch (matches the api layer's validGitBranch).
func validRefName(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '/', c == '-':
		default:
			return false
		}
	}
	return true
}
