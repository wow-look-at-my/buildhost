package retention

import "context"

// BranchLister reports the branches a repository currently has. It is the
// deleted-branch rule's only source of truth about a branch's existence, and it
// is a seam: the reclaim pass itself never talks to a remote, so the rule can be
// driven in a test without a network.
//
// The production implementation is auth.BranchLister, which answers from the
// GitHub REST API through the same base URL, HTTP client and bearer resolver the
// default-branch lookup already uses.
//
// An error means the answer is UNKNOWN, never "the repository has no branches".
// The caller keeps every release of a repository it could not ask about, because
// an unreachable API must never delete anything.
type BranchLister interface {
	LiveBranches(ctx context.Context, repoPath string) ([]string, error)
}
