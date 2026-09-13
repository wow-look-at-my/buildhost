package db

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testRepoID = "1287579802"

func mergeProject(t *testing.T, d *DB, name, repo, repoID string) *Project {
	t.Helper()
	p := &Project{Name: name, Versioning: VersioningAuto, GithubRepo: repo, GithubRepoID: repoID, GithubOwnerID: "42"}
	require.NoError(t, d.CreateProject(context.Background(), p))
	return p
}

func mergeReleases(t *testing.T, d *DB, projectID int64, versions ...string) {
	t.Helper()
	for i, v := range versions {
		require.NoError(t, d.CreateRelease(context.Background(), &Release{
			ProjectID: projectID, Version: v, VersionNum: int64(i + 1), GitBranch: "master",
		}))
	}
}

// A rename stranded the history under the old name while new publishes landed
// under the new name. The merged project must carry every release from each.
func TestProjectMerge_MovesAndRenumbersReleases(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	src := mergeProject(t, d, "log-progress-indicator/lpi", "wow-look-at-my/log-progress-indicator", testRepoID)
	dst := mergeProject(t, d, "lpi", "wow-look-at-my/lpi", testRepoID)
	mergeReleases(t, d, src.ID, "1", "2", "3")
	mergeReleases(t, d, dst.ID, "1", "2")

	plan, err := d.PlanProjectMerge(ctx, src.Name, dst.Name)
	require.NoError(t, err)
	require.True(t, plan.Applicable(), "conflicts: %v", plan.Conflicts)

	// The target's own numbering is untouched; the source's continues above it.
	require.Len(t, plan.Releases, 3)
	assert.Equal(t, "3", plan.Releases[0].NewVersion)
	assert.Equal(t, "4", plan.Releases[1].NewVersion)
	assert.Equal(t, "5", plan.Releases[2].NewVersion)

	require.NoError(t, d.ApplyProjectMerge(ctx, plan))

	surviving, err := d.ListReleases(ctx, dst.ID)
	require.NoError(t, err)
	assert.Len(t, surviving, 5, "every release of both projects survives the merge")

	_, err = d.GetProject(ctx, src.Name)
	assert.ErrorIs(t, err, ErrNotFound, "the merged-away project row is gone")
}

// The whole reason a rename is survivable: a published URL naming the old
// project keeps resolving after the merge.
func TestProjectMerge_OldNameResolvesAsAlias(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	src := mergeProject(t, d, "slopfmt", "wow-look-at-my/slopfmt", "1359392578")
	dst := mergeProject(t, d, "slopfix", "wow-look-at-my/slopfix", "1359392578")
	mergeReleases(t, d, src.ID, "1", "2")

	plan, err := d.PlanProjectMerge(ctx, src.Name, dst.Name)
	require.NoError(t, err)
	require.NoError(t, d.ApplyProjectMerge(ctx, plan))

	got, aliased, err := d.ResolveProject(ctx, "slopfmt")
	require.NoError(t, err)
	assert.True(t, aliased)
	assert.Equal(t, "slopfix", got.Name, "the old name resolves to the survivor")
}

// Merging projects that are not the same repository silently fuses unrelated
// histories, so the repo id must agree.
func TestProjectMerge_RefusesDifferentRepos(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	mergeProject(t, d, "alpha", "wow-look-at-my/alpha", "111")
	mergeProject(t, d, "beta", "wow-look-at-my/beta", "222")

	plan, err := d.PlanProjectMerge(ctx, "alpha", "beta")
	require.NoError(t, err)
	assert.False(t, plan.Applicable())
	assert.ErrorContains(t, d.ApplyProjectMerge(ctx, plan), "conflict")
}

// A site deployment on the same branch in each project has no correct winner,
// so the merge must stop rather than discard a deployment.
func TestProjectMerge_RefusesSiteBranchCollision(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	src := mergeProject(t, d, "old-name", "wow-look-at-my/old-name", testRepoID)
	dst := mergeProject(t, d, "new-name", "wow-look-at-my/new-name", testRepoID)
	_, err := d.UpsertSite(ctx, &Site{ProjectID: src.ID, Branch: "master", StorageKey: "a"})
	require.NoError(t, err)
	_, err = d.UpsertSite(ctx, &Site{ProjectID: dst.ID, Branch: "master", StorageKey: "b"})
	require.NoError(t, err)

	plan, planErr := d.PlanProjectMerge(ctx, src.Name, dst.Name)
	require.NoError(t, planErr)
	assert.False(t, plan.Applicable())
}

// The snapshot is what makes a merge recoverable on a database with no backup.
func TestSnapshotTo_WritesAUsableCopy(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	mergeProject(t, d, "snapme", "wow-look-at-my/snapme", testRepoID)

	snap := filepath.Join(t.TempDir(), "snap.db")
	require.NoError(t, d.SnapshotTo(ctx, snap))

	restored, err := Open(snap)
	require.NoError(t, err)
	defer restored.Close()

	got, err := restored.GetProject(ctx, "snapme")
	require.NoError(t, err)
	assert.Equal(t, "snapme", got.Name)
}

func TestRenameProject_KeepsOldNameResolving(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	p := mergeProject(t, d, "oldname", "wow-look-at-my/oldname", testRepoID)
	require.NoError(t, d.RenameProject(ctx, p.ID, "oldname", "newname"))

	got, aliased, err := d.ResolveProject(ctx, "oldname")
	require.NoError(t, err)
	assert.True(t, aliased)
	assert.Equal(t, "newname", got.Name)

	available, err := d.NameAvailable(ctx, "oldname")
	require.NoError(t, err)
	assert.False(t, available, "an alias keeps its name reserved against a new project")
}
