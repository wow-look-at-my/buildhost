package db

import (
	"context"
	"time"
)

// RecordBranchDeletedForRepo marks branch deleted at t on every project tied to
// githubRepo ("owner/repo"). An earlier record wins.
func (d *DB) RecordBranchDeletedForRepo(ctx context.Context, githubRepo, branch string, t time.Time) error {
	return d.q.RecordBranchDeletedForRepo(ctx, RecordBranchDeletedForRepoParams{
		Branch:     branch,
		DeletedAt:  sqliteDatetime(t),
		GithubRepo: githubRepo,
	})
}

// ClearBranchDeletedForRepo forgets a deletion when the branch exists again.
func (d *DB) ClearBranchDeletedForRepo(ctx context.Context, githubRepo, branch string) error {
	return d.q.ClearBranchDeletedForRepo(ctx, ClearBranchDeletedForRepoParams{Branch: branch, GithubRepo: githubRepo})
}

// RecordBranchDeleted marks a single project's branch deleted at t. An
// earlier record wins.
func (d *DB) RecordBranchDeleted(ctx context.Context, projectID int64, branch string, t time.Time) error {
	return d.q.RecordBranchDeleted(ctx, RecordBranchDeletedParams{ProjectID: projectID, Branch: branch, DeletedAt: sqliteDatetime(t)})
}

// ClearBranchDeleted forgets a single project's branch deletion.
func (d *DB) ClearBranchDeleted(ctx context.Context, projectID int64, branch string) error {
	return d.q.ClearBranchDeleted(ctx, ClearBranchDeletedParams{ProjectID: projectID, Branch: branch})
}

// ListDeletedBranches returns the recorded deletions for a project.
func (d *DB) ListDeletedBranches(ctx context.Context, projectID int64) ([]DeletedBranch, error) {
	return d.q.ListDeletedBranches(ctx, projectID)
}

// ListGitHubRepoProjects returns every project tied to a GitHub repo.
func (d *DB) ListGitHubRepoProjects(ctx context.Context) ([]ListGitHubRepoProjectsRow, error) {
	return d.q.ListGitHubRepoProjects(ctx)
}

// ListProjectReleaseBranches returns the distinct branches a project holds
// releases on.
func (d *DB) ListProjectReleaseBranches(ctx context.Context, projectID int64) ([]string, error) {
	return d.q.ListProjectReleaseBranches(ctx, projectID)
}
