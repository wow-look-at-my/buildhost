package retention

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-containers/set"
)

// BranchLister reports the branches a project's origin repository currently
// has. An error means the answer is unknown, and the deleted-branch rule keeps
// every release of that repository: a branch that cannot be listed is never
// treated as a branch that was deleted.
type BranchLister interface {
	ListBranches(ctx context.Context, repoPath string) (set.Set[string], error)
}

const (
	githubBranchPageSize = 100
	// githubBranchMaxPages caps one repository's pagination. Hitting the cap
	// returns an error rather than a partial set, because a truncated list
	// makes live branches look deleted.
	githubBranchMaxPages   = 40
	githubBranchHTTPBudget = 30 * time.Second
	githubBranchBodyLimit  = 4 << 20
)

// GitHubBranchLister answers branch existence from the GitHub REST API, which
// is the authoritative ref list for a repository. It caches nothing: a pass
// asks GitHub once per repository and uses that answer for the whole pass, so
// no decision is ever made from a stored branch table that a push or a delete
// has moved on from.
type GitHubBranchLister struct {
	// Bearer supplies a token for owner/repo, normally auth.BearerForRepo. A
	// private repository without one answers 404, which is an error here and
	// therefore keeps the releases.
	Bearer func(ctx context.Context, owner, repo string) string
	// BaseURL overrides the GitHub API root. Empty means api.github.com.
	BaseURL string
	// Client overrides the HTTP client. Empty means a client bounded by
	// githubBranchHTTPBudget.
	Client *http.Client
}

func (g *GitHubBranchLister) baseURL() string {
	if g.BaseURL != "" {
		return strings.TrimSuffix(g.BaseURL, "/")
	}
	return "https://api.github.com"
}

func (g *GitHubBranchLister) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: githubBranchHTTPBudget}
}

// ListBranches returns every branch name on owner/repo.
func (g *GitHubBranchLister) ListBranches(ctx context.Context, repoPath string) (set.Set[string], error) {
	owner, repo, ok := strings.Cut(repoPath, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return set.Set[string]{}, fmt.Errorf("not an owner/repo path: %q", repoPath)
	}

	bearer := ""
	if g.Bearer != nil {
		bearer = g.Bearer(ctx, owner, repo)
	}

	branches := set.New[string](githubBranchPageSize)
	for page := 1; page <= githubBranchMaxPages; page++ {
		names, returned, err := g.fetchPage(ctx, owner, repo, bearer, page)
		if err != nil {
			return set.Set[string]{}, err
		}
		branches.AddRange(names...)
		if returned < githubBranchPageSize {
			return g.checkNonEmpty(branches, repoPath)
		}
	}
	return set.Set[string]{}, fmt.Errorf("%s has more than %d branches: refusing a truncated branch list",
		repoPath, githubBranchMaxPages*githubBranchPageSize)
}

// checkNonEmpty rejects a repository that reports no branches at all. Every
// repository buildhost has published a release from has at least one, so an
// empty list is a wrong answer, and acting on it would mark every branch of
// that project deleted at once.
func (g *GitHubBranchLister) checkNonEmpty(branches set.Set[string], repoPath string) (set.Set[string], error) {
	if branches.IsEmpty() {
		return set.Set[string]{}, fmt.Errorf("%s reported no branches", repoPath)
	}
	return branches, nil
}

// fetchPage returns one page of branch names plus how many entries the page
// carried. The count drives pagination and the names drive the set, so a
// nameless entry cannot end the walk early.
func (g *GitHubBranchLister) fetchPage(ctx context.Context, owner, repo, bearer string, page int) ([]string, int, error) {
	repoPath := owner + "/" + repo
	endpoint := fmt.Sprintf("%s/repos/%s/%s/branches?per_page=%d&page=%d",
		g.baseURL(), url.PathEscape(owner), url.PathEscape(repo), githubBranchPageSize, page)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build branch request for %s: %w", repoPath, err)
	}
	req.Header.Set("User-Agent", "buildhost")
	req.Header.Set("Accept", "application/vnd.github+json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := g.client().Do(req)
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
	if err := json.NewDecoder(io.LimitReader(resp.Body, githubBranchBodyLimit)).Decode(&payload); err != nil {
		return nil, 0, fmt.Errorf("decode branches for %s: %w", repoPath, err)
	}

	names := make([]string, 0, len(payload))
	for _, b := range payload {
		if b.Name != "" {
			names = append(names, b.Name)
		}
	}
	return names, len(payload), nil
}
