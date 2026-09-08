package auth

import (
	"context"
	"log/slog"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

// A repo owns a namespace: the root project plus every "<root>/<child>" beneath
// it. Its name is mutable and its id is not (docs/security/oidc.md), so a rename
// rewrites the root of every project on that id and keeps old names as aliases.

// repoNamespaceRoot is the root a repo's namespace sits under, lowercased to
// match provisioning.
func repoNamespaceRoot(repoPath string) string {
	slash := strings.LastIndex(repoPath, "/")
	if slash < 0 || slash == len(repoPath)-1 {
		return ""
	}
	return strings.ToLower(repoPath[slash+1:])
}

// renamedNamespaceName rewrites name's root segment, keeping the child path.
// It returns "" when name already sits under root.
//
// A child whose whole path repeats the root collapses to the root: a repo named
// after its sole binary would otherwise land on the redundant "lpi/lpi".
func renamedNamespaceName(name, root string) string {
	head, rest, hasChild := strings.Cut(name, "/")
	if head == root {
		return ""
	}
	if !hasChild || rest == root {
		return root
	}
	return root + "/" + rest
}

// reconcileRepoNamespace renames a GitHub repo's projects onto the repo's
// current name. It runs on WRITE requests only, matching the rule that a read
// never mutates project state.
//
// A taken target name is NOT resolved here: that is a duplicate from a rename
// predating this reconcile, and folding release histories together has no safe
// default. `buildhost project merge` does it deliberately, under a snapshot.
func reconcileRepoNamespace(ctx context.Context, database *db.DB, repo OIDCRepoIdentity) {
	if repo.RepoID == "" || repo.RepoPath == "" {
		return
	}
	root := repoNamespaceRoot(repo.RepoPath)
	if root == "" {
		return
	}
	projects, err := database.ProjectsForRepoID(ctx, repo.RepoID)
	if err != nil {
		slog.ErrorContext(ctx, "repo namespace reconcile: list projects failed",
			"repo", repo.RepoPath, "repo_id", repo.RepoID, "error", err)
		return
	}
	for i := range projects {
		p := &projects[i]
		newName := renamedNamespaceName(p.Name, root)
		if newName == "" {
			continue
		}
		available, err := database.NameAvailable(ctx, newName)
		if err != nil {
			slog.ErrorContext(ctx, "repo namespace reconcile: name probe failed",
				"project", p.Name, "want", newName, "error", err)
			continue
		}
		if !available {
			slog.WarnContext(ctx, "repo namespace reconcile: rename blocked, merge needed",
				"project", p.Name,
				"want", newName,
				"repo", repo.RepoPath,
				"repo_id", repo.RepoID,
				"hint", "buildhost project merge --from "+p.Name+" --into "+newName,
			)
			continue
		}
		if err := database.RenameProject(ctx, p.ID, p.Name, newName); err != nil {
			slog.ErrorContext(ctx, "repo namespace reconcile: rename failed",
				"project", p.Name, "want", newName, "error", err)
			continue
		}
		slog.WarnContext(ctx, "repo namespace reconcile: project renamed to follow its repo",
			"was", p.Name,
			"now", newName,
			"repo", repo.RepoPath,
			"repo_id", repo.RepoID,
		)
	}
}
