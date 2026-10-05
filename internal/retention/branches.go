package retention

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/wow-look-at-my/go-containers/set"
)

// BranchLister returns every live branch of a GitHub repo ("owner/repo").
type BranchLister func(ctx context.Context, repoPath string) ([]string, error)

// WithBranchLister makes each run earliest sync deleted_branches against GitHub.
func (r *Retention) WithBranchLister(l BranchLister) *Retention {
	r.branches = l
	return r
}

// BranchSync reports what a sync of deleted_branches changed.
type BranchSync struct {
	Repos         int      // GitHub repos listed
	MarkedDeleted int      // branches newly recorded as deleted
	Cleared       int      // deletions forgotten because the branch exists again
	Errors        []string // repos that could not be listed; their branches were left alone
}

// SyncDeletedBranches records, for every project tied to a GitHub repo, each
// release branch GitHub has. It covers deletions the webhook missed. The
// deletion time is when buildhost earliest saw the branch gone, so the TTL
// of a branch deleted long ago starts at its earliest sync. A repo that
// cannot be listed changes nothing.
func (r *Retention) SyncDeletedBranches(ctx context.Context) (BranchSync, error) {
	var rep BranchSync
	projects, err := r.db.ListGitHubRepoProjects(ctx)
	if err != nil {
		return rep, fmt.Errorf("list github projects: %w", err)
	}

	now := r.clock()
	live := make(map[string]set.Set[string])
	failed := set.New[string]()
	for _, p := range projects {
		if failed.Contains(p.GithubRepo) {
			continue
		}
		branches, ok := live[p.GithubRepo]
		if !ok {
			names, err := r.branches(ctx, p.GithubRepo)
			if err != nil {
				failed.Add(p.GithubRepo)
				rep.Errors = append(rep.Errors, err.Error())
				slog.ErrorContext(ctx, "retention: branch sync skipped a repo; its deleted branches are not recorded",
					"repo", p.GithubRepo, "err", err)
				continue
			}
			branches = set.New[string](len(names))
			for _, n := range names {
				branches.Add(n)
			}
			live[p.GithubRepo] = branches
			rep.Repos++
		}

		recorded, err := r.db.ListDeletedBranches(ctx, p.ID)
		if err != nil {
			return rep, fmt.Errorf("list deleted branches of %s: %w", p.Name, err)
		}
		known := set.New[string](len(recorded))
		for _, d := range recorded {
			if branches.Contains(d.Branch) {
				if err := r.db.ClearBranchDeleted(ctx, p.ID, d.Branch); err != nil {
					return rep, fmt.Errorf("clear deleted branch %s of %s: %w", d.Branch, p.Name, err)
				}
				rep.Cleared++
				continue
			}
			known.Add(d.Branch)
		}

		used, err := r.db.ListProjectReleaseBranches(ctx, p.ID)
		if err != nil {
			return rep, fmt.Errorf("list release branches of %s: %w", p.Name, err)
		}
		for _, b := range used {
			if b == p.DefaultBranch || branches.Contains(b) || known.Contains(b) {
				continue
			}
			if err := r.db.RecordBranchDeleted(ctx, p.ID, b, now); err != nil {
				return rep, fmt.Errorf("record deleted branch %s of %s: %w", b, p.Name, err)
			}
			rep.MarkedDeleted++
		}
	}
	return rep, nil
}
