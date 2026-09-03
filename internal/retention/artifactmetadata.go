package retention

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// gitHubAPIBase is the artifact-metadata API host, swapped in tests.
var gitHubAPIBase = "https://api.github.com"

var metadataHTTPClient = &http.Client{Timeout: 20 * time.Second}

// RecordDeleter marks an org's artifact-metadata storage records deleted.
type RecordDeleter interface {
	// MarkDeleted reports the artifact with digest sha256Hex, published as
	MarkDeleted(ctx context.Context, githubRepo, project, version, sha256Hex string) error
}

// GitHubRecordDeleter posts status: deleted to GitHub's artifact-metadata API,
// authenticating as buildhost itself (GitHub App installation token, else the
// static PAT) via the bearer function it is constructed with.
type GitHubRecordDeleter struct {
	// RegistryURL is buildhost's own public base URL (e.g. https://pazer.build).
	RegistryURL string
	// Bearer resolves a GitHub credential for owner/repo, "" when unconfigured.
	Bearer func(ctx context.Context, owner, repo string) string
}

// MarkDeleted implements RecordDeleter.
func (g *GitHubRecordDeleter) MarkDeleted(ctx context.Context, githubRepo, project, version, sha256Hex string) error {
	owner, repo, ok := strings.Cut(githubRepo, "/")
	if !ok || owner == "" || repo == "" {
		return fmt.Errorf("project %q has an unusable github_repo %q", project, githubRepo)
	}
	if g.RegistryURL == "" {
		return fmt.Errorf("no registry URL configured (set BUILDHOST_PRIMARY_DOMAIN)")
	}

	bearer := ""
	if g.Bearer != nil {
		bearer = g.Bearer(ctx, owner, repo)
	}
	if bearer == "" {
		return fmt.Errorf("no GitHub credential for %s (configure a GitHub App or BUILDHOST_GITHUB_TOKEN)", githubRepo)
	}

	// Same endpoint the publish records through: a record is identified by
	// (name, digest, registry_url), so re-posting that triple with a new status
	// updates it. github_repository is the repo NAME only -- its pattern is
	body, err := json.Marshal(map[string]any{
		"name":              project,
		"version":           version,
		"digest":            "sha256:" + sha256Hex,
		"registry_url":      g.RegistryURL,
		"repository":        project,
		"github_repository": repo,
		"status":            "deleted",
		"return_records":    false,
	})
	if err != nil {
		return fmt.Errorf("marshal storage record: %w", err)
	}

	url := gitHubAPIBase + "/orgs/" + owner + "/artifacts/metadata/storage-record"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "buildhost")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)

	resp, err := metadataHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("post storage record: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := make([]byte, 512)
		n, _ := resp.Body.Read(snippet)
		msg := strings.TrimSpace(string(snippet[:n]))
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("HTTP %d marking %s@%s deleted: %s -- the GitHub App (or token) needs artifact-metadata write on %s", resp.StatusCode, project, version, msg, owner)
		}
		return fmt.Errorf("HTTP %d marking %s@%s deleted: %s", resp.StatusCode, project, version, msg)
	}
	return nil
}
