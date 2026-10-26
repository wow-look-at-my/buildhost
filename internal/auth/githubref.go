// prRefName matches the ref name a pull_request build records: "<number>/merge".

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var githubRefHTTPClient = &http.Client{Timeout: 10 * time.Second}

// prRefName matches the ref name a pull_request build records: "<number>/merge".
var prRefName = regexp.MustCompile(`^([0-9]+)/(merge|head)$`)

// GitHubRepoDefaultBranch asks GitHub for the default branch of repoPath. It
// does not cache.
func GitHubRepoDefaultBranch(ctx context.Context, repoPath string) (string, error) {
	if !validRepoPath(repoPath) {
		return "", fmt.Errorf("invalid repository %q", repoPath)
	}
	var body struct {
		DefaultBranch string `json:"default_branch"`
	}
	status, err := githubRefGet(ctx, repoPath, "", &body)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("GitHub answered %d for repository %s", status, repoPath)
	}
	if !validRefName(body.DefaultBranch) {
		return "", fmt.Errorf("GitHub reported no valid default branch for %s", repoPath)
	}
	return body.DefaultBranch, nil
}

// GitHubRefPresent reports whether name still exists on repoPath. A build
// records GITHUB_REF_NAME, so name is a branch, a tag, or "<pr>/merge". A pull
// request ref is present while its pull request is open. An error means the
// answer is unknown.
func GitHubRefPresent(ctx context.Context, repoPath, name string) (bool, error) {
	if !validRepoPath(repoPath) {
		return false, fmt.Errorf("invalid repository %q", repoPath)
	}
	if !validRefName(name) || strings.Contains(name, "..") {
		return false, fmt.Errorf("invalid ref name %q", name)
	}
	if m := prRefName.FindStringSubmatch(name); m != nil {
		var pr struct {
			State string `json:"state"`
		}
		status, err := githubRefGet(ctx, repoPath, "/pulls/"+m[1], &pr)
		if err != nil {
			return false, err
		}
		switch {
		case status == http.StatusNotFound:
			return false, nil
		case status != http.StatusOK:
			return false, fmt.Errorf("GitHub answered %d for pull request %s#%s", status, repoPath, m[1])
		case pr.State == "":
			return false, fmt.Errorf("GitHub reported no state for pull request %s#%s", repoPath, m[1])
		}
		return pr.State == "open", nil
	}
	for _, path := range []string{"/branches/" + name, "/git/ref/tags/" + name} {
		status, err := githubRefGet(ctx, repoPath, path, nil)
		if err != nil {
			return false, err
		}
		if status == http.StatusOK {
			return true, nil
		}
		if status != http.StatusNotFound {
			return false, fmt.Errorf("GitHub answered %d for %s%s", status, repoPath, path)
		}
	}
	return false, nil
}

// It returns the status for the caller to classify.
func githubRefGet(ctx context.Context, repoPath, path string, out any) (int, error) {
	owner, repo, _ := strings.Cut(repoPath, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gitHubAPIBase+"/repos/"+repoPath+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "buildhost")
	req.Header.Set("Accept", "application/vnd.github+json")
	if bearer := bearerForRepo(ctx, owner, repo); bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := githubRefHTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK && out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
			return 0, fmt.Errorf("decode %s%s: %w", repoPath, path, err)
		}
	}
	return resp.StatusCode, nil
}
