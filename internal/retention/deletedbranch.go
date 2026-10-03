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
// repository. Every build on such a branch goes, its tip included, so a branch
// that was deleted upstream ages out completely instead of leaving its newest
// build behind forever.
//
// Everything it cannot establish it refuses to delete. A release with no
// recorded branch, a project with no recorded repository, and a repository
// whose branch list could not be read all end up in the report as kept, with
// the reason, rather than being reclaimed on an assumption.
//
// The default branch is dropped here: the apex latest resolves against it, so it
// is never treated as a dead branch however the remote answers.
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
		live map[string]bool
		err  error
	}
	// One answer per repository for the whole pass, however many branches and
	// builds of it are candidates.
	byRepo := make(map[string]lookup, 4)
	seenMsg := set.New[string]()

	// noteFailure records that a release was KEPT because branch existence could
	// not be established. It logs one line per distinct cause, naming the
	// repository it could not ask about.
	noteFailure := func(repoPath, reason string) {
		rep.BranchLookupsFailed++
		msg := reason
		if repoPath != "" {
			msg = fmt.Sprintf("%s: %s", repoPath, reason)
		}
		if seenMsg.Add(msg) {
			rep.BranchLookupErrors = append(rep.BranchLookupErrors, msg)
			slog.WarnContext(ctx, "retention: branch liveness undetermined, releases kept",
				"repository", repoPath, "reason", reason)
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
			noteFailure("project "+row.ProjectName,
				"no github_repo recorded, so the branches it was built from cannot be listed")
			continue
		}

		l, cached := byRepo[row.GithubRepo]
		if !cached {
			names, err := r.branches.LiveBranches(ctx, row.GithubRepo)
			l = lookup{live: make(map[string]bool, len(names)), err: err}
			for _, name := range names {
				l.live[name] = true
			}
			byRepo[row.GithubRepo] = l
		}
		if l.err != nil {
			noteFailure(row.GithubRepo, l.err.Error())
			continue
		}
		if l.live[row.GitBranch] {
			continue // the branch is still on the remote
		}
		eligible = append(eligible, ref)
	}

	return eligible, nil
}
