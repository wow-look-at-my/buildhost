package sites

// Ref resolution: turning the "<ref>[/<path>]" remainder of a site URL into the

import (
	"context"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

// defaultBranch is the branch a project's bare root resolves to: its
// projects.default_branch (learned from GitHub on publish, e.g. "main"),
func defaultBranch(project *db.Project) string {
	if project != nil && project.DefaultBranch != "" {
		return project.DefaultBranch
	}
	return db.LatestBranch
}

// resolveRootBranch returns the branch the bare site root should resolve to. It
// prefers the project's default branch (defaultBranch), but only when a site has
// actually been published there. projects.default_branch is a best-effort hint
// learned from GitHub on publish; it can lag at the seed "master" -- e.g. until a
// GitHub-OIDC publish corrects it, or when buildhost can't reach a private repo
// to learn its real default -- in which case the sites may all live on a branch
// (commonly "main") the hint doesn't name. Blindly trusting it then bounces the
func resolveRootBranch(ctx context.Context, database *db.DB, project *db.Project) string {
	preferred := defaultBranch(project)
	if database == nil || siteExists(ctx, database, project.ID, preferred) {
		return preferred
	}
	for _, b := range [...]string{"main", db.LatestBranch} {
		if b != preferred && siteExists(ctx, database, project.ID, b) {
			return b
		}
	}
	if sites, err := database.ListSites(ctx, project.ID); err == nil && len(sites) > 0 {
		return sites[0].Branch // ListSites is ordered updated_at DESC
	}
	return preferred // no sites at all -- keep the default (Serve 404s as before)
}

func siteExists(ctx context.Context, database *db.DB, projectID int64, branch string) bool {
	if branch == "" {
		return false
	}
	_, err := database.GetSite(ctx, projectID, branch)
	return err == nil
}

func joinPathParts(head, tail string) string {
	if tail == "" {
		return head
	}
	return head + "/" + tail
}

// splitSiteBranch splits a combined "<ref>[/<path>]" remainder into the branch
// serving it and the file path, by LONGEST match against the project's existing
// site rows. Branch names may legally contain "/" (claude/foo), so no purely
func splitSiteBranch(ctx context.Context, database *db.DB, projectID int64, remainder string) (branch, filePath string, ok bool) {
	segs := strings.Split(remainder, "/")
	for i := len(segs); i >= 1; i-- {
		cand := strings.Join(segs[:i], "/")
		if !validSiteBranch(cand) {
			continue
		}
		if siteExists(ctx, database, projectID, cand) {
			return cand, strings.Join(segs[i:], "/"), true
		}
	}
	if b, okc := resolveCommitRef(ctx, database, projectID, segs[0]); okc {
		return b, strings.Join(segs[1:], "/"), true
	}
	return "", "", false
}

// refNamesBranch reports whether the ref as SPELLED in the URL is the branch
func refNamesBranch(ref, branch string) bool {
	return ref == branch || strings.HasPrefix(ref, branch+"/")
}

// minCommitRefLen is the shortest abbreviated sha accepted as a commit ref.
const minCommitRefLen = 7

// resolveCommitRef resolves a git commit (full sha or an abbreviation of at
// least minCommitRefLen) to the branch whose CURRENT deployment was built from
func resolveCommitRef(ctx context.Context, database *db.DB, projectID int64, seg string) (branch string, ok bool) {
	if database == nil || !looksLikeCommit(seg) {
		return "", false
	}
	sites, err := database.SitesByCommitPrefix(ctx, projectID, strings.ToLower(seg))
	if err != nil || len(sites) == 0 {
		return "", false
	}
	return sites[0].Branch, true
}

// looksLikeCommit reports whether a path segment could be a git commit sha:
func looksLikeCommit(s string) bool {
	if len(s) < minCommitRefLen || len(s) > 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// validSiteBranch mirrors the api layer's validGitBranch (and auth's
func validSiteBranch(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '/', c == '-':
		default:
			return false
		}
	}
	return true
}
