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

	// Nothing published under the names may break.
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

// A project made under the raw `.github` name moves to `github`, the only name its token may write.
func TestReconcileRepoNamespace_MovesAPunctuatedNameToTheDerivedOne(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	p := reconcileProject(t, d, ".github", "wow-look-at-my/.github", reconcileRepoID)
	reconcileRepoNamespace(ctx, d, OIDCRepoIdentity{
		RepoPath: "wow-look-at-my/.github", RepoID: reconcileRepoID, OwnerID: "42",
	})

	moved, aliased, err := d.ResolveProject(ctx, "github")
	require.NoError(t, err)
	assert.False(t, aliased)
	assert.Equal(t, p.ID, moved.ID)
	assert.True(t, oidcAuthorizesProject(RepoProjectName(".github"), moved.Name))
}

// .github provisions github, so a publish from it must leave github alone.
func TestReconcileRepoNamespace_KeepsTheProjectOfADotRepo(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	p := reconcileProject(t, d, "github", "wow-look-at-my/.github", reconcileRepoID)
	reconcileRepoNamespace(ctx, d, OIDCRepoIdentity{
		RepoPath: "wow-look-at-my/.github", RepoID: reconcileRepoID, OwnerID: "42",
	})

	still, err := d.GetProject(ctx, "github")
	require.NoError(t, err)
	assert.Equal(t, p.ID, still.ID)
}

// A project moved onto the raw repo name moves back, although its old name is its own alias.
func TestReconcileRepoNamespace_MovesADotRepoProjectBack(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	p := reconcileProject(t, d, "github", "wow-look-at-my/.github", reconcileRepoID)
	require.NoError(t, d.RenameProject(ctx, p.ID, "github", ".github"))

	reconcileRepoNamespace(ctx, d, OIDCRepoIdentity{
		RepoPath: "wow-look-at-my/.github", RepoID: reconcileRepoID, OwnerID: "42",
	})

	back, aliased, err := d.ResolveProject(ctx, "github")
	require.NoError(t, err)
	assert.False(t, aliased, "github is the project's name again, not an alias")
	assert.Equal(t, p.ID, back.ID)

	old, aliased, err := d.ResolveProject(ctx, ".github")
	require.NoError(t, err)
	assert.True(t, aliased, "the wrong name still resolves")
	assert.Equal(t, "github", old.Name)
}

// A name that another project holds as an alias stays taken.
func TestReconcileRepoNamespace_LeavesAnotherProjectsAliasAlone(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	other := reconcileProject(t, d, "slopfix", "wow-look-at-my/slopfix", "999")
	require.NoError(t, d.RenameProject(ctx, other.ID, "slopfix", "slopfix2"))
	stranded := reconcileProject(t, d, "slopfmt", "wow-look-at-my/slopfmt", reconcileRepoID)

	reconcileRepoNamespace(ctx, d, OIDCRepoIdentity{
		RepoPath: "wow-look-at-my/slopfix", RepoID: reconcileRepoID, OwnerID: "42",
	})

	still, err := d.GetProject(ctx, "slopfmt")
	require.NoError(t, err)
	assert.Equal(t, stranded.ID, still.ID)
	owner, aliased, err := d.ResolveProject(ctx, "slopfix")
	require.NoError(t, err)
	assert.True(t, aliased)
	assert.Equal(t, other.ID, owner.ID)
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
