package retention

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/wow-look-at-my/go-containers/set"
)

// planDeletedBranch selects the releases the deleted-branch rule reclaims: a
// published release is eligible when its age is past Config.DeletedBranchAge
// AND the branch it was built from no longer exists on the project's origin
// repository. Applied on every pass, it drains a deleted branch down to the one
// build a download URL still resolves.
//
// Everything it cannot establish it refuses to delete. A release with no
// recorded branch, a project with no recorded repository, and a repository
// whose branch list could not be read all end up in the report as kept, with
// the reason, rather than being reclaimed on an assumption.
//
// The candidate query has already removed every branch tip, so nothing reaching
// this function backs a `dl` slot. The default branch is dropped here as well,
// which makes the apex latest safe twice over.
func (r *Retention) planDeletedBranch(ctx context.Context, rep *Report) ([]ReleaseRef, error) {
	if r.cfg.DeletedBranchAge <= 0 {
		return nil, nil // the rule is switched off
	}

	cutoff := r.clock().Add(-r.cfg.DeletedBranchAge)
	rows, err := r.db.ListDeletedBranchCandidates(ctx, cutoff)
	if err != nil {
		return nil, fmt.Errorf("list deleted-branch candidates: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}

	if r.branches == nil {
		rep.BranchLookupsFailed = len(rows)
		rep.BranchLookupErrors = append(rep.BranchLookupErrors,
			"no branch lister configured: branch existence cannot be established, so the deleted-branch rule reclaimed nothing")
		slog.WarnContext(ctx, "retention: deleted-branch rule skipped", "candidates", len(rows), "reason", "no branch lister configured")
		return nil, nil
	}

	type lookup struct {
		live set.Set[string]
		err  error
	}
	byRepo := make(map[string]lookup)
	seenErr := set.New[string]()
	noteError := func(msg string) {
		rep.BranchLookupsFailed++
		if seenErr.Add(msg) {
			rep.BranchLookupErrors = append(rep.BranchLookupErrors, msg)
		}
	}

	var eligible []ReleaseRef
	for _, row := range rows {
		ref := ReleaseRef{
			ID: row.ID, ProjectID: row.ProjectID, ProjectName: row.ProjectName,
			Branch: row.GitBranch, Version: row.Version,
		}

		if row.GitBranch == "" {
			// No branch was ever recorded for this build. That is not evidence
			// the branch is gone, so it is listed for an operator instead.
			rep.UnknownBranchReleases = append(rep.UnknownBranchReleases, ref)
			continue
		}
		if row.GitBranch == row.DefaultBranch {
			continue // the default branch is never reclaimed
		}
		if row.GithubRepo == "" {
			noteError(fmt.Sprintf("project %q records no github_repo, so the branches of %q cannot be listed", row.ProjectName, row.GitBranch))
			continue
		}

		l, cached := byRepo[row.GithubRepo]
		if !cached {
			l.live, l.err = r.branches.ListBranches(ctx, row.GithubRepo)
			byRepo[row.GithubRepo] = l
		}
		if l.err != nil {
			noteError(l.err.Error())
			continue
		}
		if l.live.Contains(row.GitBranch) {
			continue // the branch is still on the remote
		}
		eligible = append(eligible, ref)
	}

	return eligible, nil
}
