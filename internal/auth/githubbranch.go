package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// GitHubActionsIssuer is the canonical GitHub Actions OIDC issuer. Default-branch
const GitHubActionsIssuer = "https://token.actions.githubusercontent.com"

// gitHubAPIBase is the GitHub REST base. A var (not const) so tests can point it
var gitHubAPIBase = "https://api.github.com"

// githubToken, when set (BUILDHOST_GITHUB_TOKEN), authenticates default-branch
var (
	githubToken   string
	githubTokenMu sync.RWMutex
)

// SetGitHubToken configures the token used for default-branch lookups. Called
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
	owner, repo, _ := strings.Cut(repoPath, "/")
	bearer := bearerForRepo(ctx, owner, repo)

	// Bound the lookup so a slow github.com never stalls a publish for the full
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

const (
	// branchListPageSize is the largest page GitHub serves for a branch list.
	branchListPageSize = 100
	// branchListMaxPages caps one repository's pagination. Hitting the cap is an
	// error rather than a partial list, because a truncated list makes a live
	// branch look deleted.
	branchListMaxPages = 40
	// branchListBudget bounds one repository's whole paginated walk. It is
	// larger than branchLookupBudget because it may cover several pages.
	branchListBudget  = 30 * time.Second
	branchListBodyCap = 4 << 20
)

// BranchLister answers branch existence from GitHub, the authoritative ref list
// for a repository. It satisfies the retention package's branch-liveness seam,
// so a reclaim pass asks the same client, base URL and bearer resolver the
// default-branch lookup already uses.
//
// It caches nothing: a pass asks once per repository and holds that answer for
// the pass, so no decision is ever made from a stored branch table that a push
// or a branch deletion has moved on from.
type BranchLister struct{}

// LiveBranches implements retention's branch-liveness lookup.
func (BranchLister) LiveBranches(ctx context.Context, repoPath string) ([]string, error) {
	return LiveBranches(ctx, repoPath)
}

// LiveBranches returns every branch name owner/repo currently has, walking
// every page so a branch past the first one is never mistaken for a deleted
// branch. Any error means the answer is unknown, and the caller must not treat
// an unknown repository as one whose branches were deleted.
func LiveBranches(ctx context.Context, repoPath string) ([]string, error) {
	if !validRepoPath(repoPath) {
		return nil, fmt.Errorf("not an owner/repo path: %q", repoPath)
	}
	owner, repo, _ := strings.Cut(repoPath, "/")
	bearer := bearerForRepo(ctx, owner, repo)

	ctx, cancel := context.WithTimeout(ctx, branchListBudget)
	defer cancel()

	names := make([]string, 0, branchListPageSize)
	for page := 1; page <= branchListMaxPages; page++ {
		pageNames, entries, err := fetchBranchListPage(ctx, repoPath, bearer, page)
		if err != nil {
			return nil, err
		}
		names = append(names, pageNames...)
		if entries < branchListPageSize {
			if len(names) == 0 {
				// Every repository buildhost published a release from has at
				// least one branch, so an empty answer is a wrong answer.
				// Acting on it would mark every branch of that project deleted
				// at once, so it fails closed instead.
				return nil, fmt.Errorf("%s reported no branches", repoPath)
			}
			return names, nil
		}
	}
	return nil, fmt.Errorf("%s has more than %d branches: refusing a truncated branch list",
		repoPath, branchListMaxPages*branchListPageSize)
}

// fetchBranchListPage returns one page's branch names plus how many entries the
// page carried. The entry count drives pagination, so a nameless entry cannot
// end the walk early.
func fetchBranchListPage(ctx context.Context, repoPath, bearer string, page int) ([]string, int, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/branches?per_page=%d&page=%d",
		gitHubAPIBase, repoPath, branchListPageSize, page)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build branch-list request for %s: %w", repoPath, err)
	}
	req.Header.Set("User-Agent", "buildhost")
	req.Header.Set("Accept", "application/vnd.github+json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := githubBranchHTTPClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("list branches for %s: %w", repoPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("list branches for %s: HTTP %d", repoPath, resp.StatusCode)
	}

	var payload []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, branchListBodyCap)).Decode(&payload); err != nil {
		return nil, 0, fmt.Errorf("decode branch list for %s: %w", repoPath, err)
	}

	names := make([]string, 0, len(payload))
	for _, b := range payload {
		if b.Name != "" {
			names = append(names, b.Name)
		}
	}
	return names, len(payload), nil
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
