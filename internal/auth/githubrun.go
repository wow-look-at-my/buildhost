package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var githubRunHTTPClient = &http.Client{Timeout: 10 * time.Second}

// GitHubRunEnded asks GitHub whether a workflow run of repoPath is complete.
// It returns the time the run completed, and done=false while it runs. An
// error means the answer is unknown: the caller asks again later.
func GitHubRunEnded(ctx context.Context, repoPath, runID string) (time.Time, bool, error) {
	if !validRepoPath(repoPath) || runID == "" || strings.Trim(runID, "0123456789") != "" {
		return time.Time{}, false, fmt.Errorf("invalid run %s#%s", repoPath, runID)
	}
	owner, repo, _ := strings.Cut(repoPath, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gitHubAPIBase+"/repos/"+repoPath+"/actions/runs/"+runID, nil)
	if err != nil {
		return time.Time{}, false, err
	}
	req.Header.Set("User-Agent", "buildhost")
	req.Header.Set("Accept", "application/vnd.github+json")
	if bearer := bearerForRepo(ctx, owner, repo); bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := githubRunHTTPClient.Do(req)
	if err != nil {
		return time.Time{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return time.Time{}, false, fmt.Errorf("GitHub answered %d for run %s#%s: %s", resp.StatusCode, repoPath, runID, strings.TrimSpace(string(body)))
	}
	var run struct {
		Status    string    `json:"status"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&run); err != nil {
		return time.Time{}, false, fmt.Errorf("decode run %s#%s: %w", repoPath, runID, err)
	}
	if run.Status != "completed" {
		return time.Time{}, false, nil
	}
	if run.UpdatedAt.IsZero() {
		return time.Time{}, false, fmt.Errorf("run %s#%s is completed but carries no updated_at", repoPath, runID)
	}
	return run.UpdatedAt, true, nil
}
