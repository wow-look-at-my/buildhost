// RefChecker answers what a GitHub repository holds.

package retention

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

// RefChecker answers what a GitHub repository holds. An error means the
// answer is unknown, and the pass keeps that branch.
type RefChecker interface {
	DefaultBranch(ctx context.Context, repo string) (string, error)
	RefPresent(ctx context.Context, repo, name string) (bool, error)
}

// GitHubRefChecker asks GitHub through the mirror.
type GitHubRefChecker struct{}

func (GitHubRefChecker) DefaultBranch(ctx context.Context, repo string) (string, error) {
	return auth.GitHubRepoDefaultBranch(ctx, repo)
}

func (GitHubRefChecker) RefPresent(ctx context.Context, repo, name string) (bool, error) {
	return auth.GitHubRefPresent(ctx, repo, name)
}

// The states of a BranchCheck. Only BranchDeleted releases are evicted.
const (
	BranchDeleted = "deleted"
	BranchPresent = "present"
	BranchDefault = "default"
	BranchUnknown = "unknown"
)

// BranchCheck is the answer for one non-default branch of one project.
type BranchCheck struct {
	ProjectName string
	GithubRepo  string
	Branch      string
	State       string
	Error       string
	Releases    int
}

// PlanDeletedBranches computes what RunDeletedBranches would delete, and
// changes nothing.
func (r *Retention) PlanDeletedBranches(ctx context.Context, rc RefChecker) (Report, error) {
	return r.deletedBranches(ctx, rc, false)
}

// RunDeletedBranches deletes every release older than the recency guard on a
// non-default branch that GitHub confirms is gone from the project's repo.
func (r *Retention) RunDeletedBranches(ctx context.Context, rc RefChecker) (Report, error) {
	return r.deletedBranches(ctx, rc, r.cfg.Enforce)
}

func (r *Retention) deletedBranches(ctx context.Context, rc RefChecker, enforce bool) (Report, error) {
	rep := Report{Enforced: enforce}
	rows, err := r.db.ListNonDefaultBranchReleases(ctx, r.clock().Add(-r.cfg.RecencyGuard))
	if err != nil {
		return rep, fmt.Errorf("list non-default branch releases: %w", err)
	}

	type repoAnswer struct {
		defaultBranch string
		err           error
	}
	repos := map[string]repoAnswer{}
	checks := map[string]int{} // project + branch -> index in rep.BranchChecks
	var ids []int64

	for _, row := range rows {
		key := row.ProjectName + "\x00" + row.GitBranch
		i, seen := checks[key]
		if !seen {
			ans, ok := repos[row.GithubRepo]
			if !ok {
				def, err := rc.DefaultBranch(ctx, row.GithubRepo)
				ans = repoAnswer{defaultBranch: def, err: err}
				repos[row.GithubRepo] = ans
			}
			c := BranchCheck{ProjectName: row.ProjectName, GithubRepo: row.GithubRepo, Branch: row.GitBranch}
			switch {
			case ans.err != nil:
				c.State, c.Error = BranchUnknown, ans.err.Error()
			case row.GitBranch == ans.defaultBranch:
				c.State = BranchDefault
			default:
				present, err := rc.RefPresent(ctx, row.GithubRepo, row.GitBranch)
				switch {
				case err != nil:
					c.State, c.Error = BranchUnknown, err.Error()
				case present:
					c.State = BranchPresent
				default:
					c.State = BranchDeleted
				}
			}
			if c.State == BranchUnknown {
				slog.ErrorContext(ctx, "retention: kept builds of a branch GitHub did not confirm as deleted",
					"project", c.ProjectName, "repo", c.GithubRepo, "branch", c.Branch, "err", c.Error)
			}
			i = len(rep.BranchChecks)
			checks[key] = i
			rep.BranchChecks = append(rep.BranchChecks, c)
		}
		rep.BranchChecks[i].Releases++
		if rep.BranchChecks[i].State == BranchDeleted {
			rep.DeletedBranchReleases = append(rep.DeletedBranchReleases,
				ReleaseRef{ID: row.ID, ProjectID: row.ProjectID, ProjectName: row.ProjectName, Branch: row.GitBranch, Version: row.Version})
			ids = append(ids, row.ID)
		}
	}

	return rep, r.evict(ctx, &rep, rep.DeletedBranchReleases, ids, enforce)
}
