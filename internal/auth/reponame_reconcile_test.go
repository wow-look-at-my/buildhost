package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

const reconcileRepoID = "1287579802"

func reconcileProject(t *testing.T, d *db.DB, name, repo, repoID string) *db.Project {
	t.Helper()
	p := &db.Project{Name: name, Versioning: db.VersioningAuto, GithubRepo: repo, GithubRepoID: repoID, GithubOwnerID: "42"}
	require.NoError(t, d.CreateProject(context.Background(), p))
	return p
}

// The whole point: a publish under the repo's new name must find the renamed
// project, rather than provisioning a second one beside it.
func TestReconcileRepoNamespace_RenamesTheWholeFamily(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	root := reconcileProject(t, d, "slopfmt", "wow-look-at-my/slopfmt", reconcileRepoID)
	child := reconcileProject(t, d, "slopfmt/probe", "wow-look-at-my/slopfmt", reconcileRepoID)

	reconcileRepoNamespace(ctx, d, OIDCRepoIdentity{
		RepoPath: "wow-look-at-my/slopfix", RepoID: reconcileRepoID, OwnerID: "42",
	})

	renamed, err := d.GetProject(ctx, "slopfix")
	require.NoError(t, err)
	assert.Equal(t, root.ID, renamed.ID)

	renamedChild, err := d.GetProject(ctx, "slopfix/probe")
	require.NoError(t, err)
	assert.Equal(t, child.ID, renamedChild.ID, "a child follows its root")

	// Nothing published under the old names may break.
	old, aliased, err := d.ResolveProject(ctx, "slopfmt")
	require.NoError(t, err)
	assert.True(t, aliased)
	assert.Equal(t, "slopfix", old.Name)

	oldChild, aliased, err := d.ResolveProject(ctx, "slopfmt/probe")
	require.NoError(t, err)
	assert.True(t, aliased)
	assert.Equal(t, "slopfix/probe", oldChild.Name)
}

// A taken target name is a duplicate from a rename older than this reconcile.
// Folding release histories is the merge's job, never a side effect of a publish.
func TestReconcileRepoNamespace_LeavesADuplicateAlone(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	stranded := reconcileProject(t, d, "slopfmt", "wow-look-at-my/slopfmt", reconcileRepoID)
	current := reconcileProject(t, d, "slopfix", "wow-look-at-my/slopfix", reconcileRepoID)

	reconcileRepoNamespace(ctx, d, OIDCRepoIdentity{
		RepoPath: "wow-look-at-my/slopfix", RepoID: reconcileRepoID, OwnerID: "42",
	})

	still, err := d.GetProject(ctx, "slopfmt")
	require.NoError(t, err)
	assert.Equal(t, stranded.ID, still.ID, "the stranded project is untouched")

	kept, err := d.GetProject(ctx, "slopfix")
	require.NoError(t, err)
	assert.Equal(t, current.ID, kept.ID, "the live project keeps its releases")
}

// A repo carrying no id cannot be identified across a rename, so nothing moves.
func TestReconcileRepoNamespace_IgnoresAnIDLessToken(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	p := reconcileProject(t, d, "oldname", "wow-look-at-my/oldname", reconcileRepoID)
	reconcileRepoNamespace(ctx, d, OIDCRepoIdentity{RepoPath: "wow-look-at-my/newname", OwnerID: "42"})

	still, err := d.GetProject(ctx, "oldname")
	require.NoError(t, err)
	assert.Equal(t, p.ID, still.ID)
}
