package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

const mergeTestRepoID = "1287579802"

// newMergeServer is newTestServer with a real DBPath, which is where the
// pre-merge snapshot lands. The shared helper leaves it empty, and a snapshot
// would then be written beside the test binary.
func newMergeServer(t *testing.T) (*Server, *db.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	database, err := db.Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })

	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)

	srv := New(config.Config{DBPath: dbPath, DataDir: dir}, database, store, BuildInfo{Version: "test"})
	return srv, database, dir
}

func mergeTestProject(t *testing.T, d *db.DB, name, repo, repoID string) *db.Project {
	t.Helper()
	p := &db.Project{Name: name, Versioning: db.VersioningAuto, GithubRepo: repo, GithubRepoID: repoID, GithubOwnerID: "42"}
	require.NoError(t, d.CreateProject(context.Background(), p))
	return p
}

func mergeTestReleases(t *testing.T, d *db.DB, projectID int64, versions ...string) {
	t.Helper()
	for i, v := range versions {
		require.NoError(t, d.CreateRelease(context.Background(), &db.Release{
			ProjectID: projectID, Version: v, VersionNum: int64(i + 1), GitBranch: "master",
		}))
	}
}

func TestAdminDuplicates_ReportsASplitNamespace(t *testing.T) {
	t.Serial()
	srv, d, _ := newMergeServer(t)

	stranded := mergeTestProject(t, d, "slopfmt", "wow-look-at-my/slopfmt", mergeTestRepoID)
	mergeTestProject(t, d, "slopfix", "wow-look-at-my/slopfix", mergeTestRepoID)
	mergeTestReleases(t, d, stranded.ID, "1", "2")

	rec := serve(srv, "GET", "/api/duplicates", nil)
	require.Equal(t, 200, rec.Code)

	var got struct {
		Groups []DuplicateGroup `json:"groups"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Groups, 1)
	assert.Equal(t, mergeTestRepoID, got.Groups[0].RepoID)
	assert.Len(t, got.Groups[0].Projects, 2)
}

// A repo whose projects all sit under its own root is healthy. Reporting it
// would bury the real splits in noise.
func TestAdminDuplicates_IgnoresAHealthyNamespace(t *testing.T) {
	t.Serial()
	srv, d, _ := newMergeServer(t)

	mergeTestProject(t, d, "slopfix", "wow-look-at-my/slopfix", mergeTestRepoID)
	mergeTestProject(t, d, "slopfix/probe", "wow-look-at-my/slopfix", mergeTestRepoID)

	rec := serve(srv, "GET", "/api/duplicates", nil)
	require.Equal(t, 200, rec.Code)

	var got struct {
		Groups []DuplicateGroup `json:"groups"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got.Groups)
}

func TestAdminMergePlan_PreviewChangesNothing(t *testing.T) {
	t.Serial()
	srv, d, _ := newMergeServer(t)

	src := mergeTestProject(t, d, "slopfmt", "wow-look-at-my/slopfmt", mergeTestRepoID)
	dst := mergeTestProject(t, d, "slopfix", "wow-look-at-my/slopfix", mergeTestRepoID)
	mergeTestReleases(t, d, src.ID, "1", "2")
	mergeTestReleases(t, d, dst.ID, "1")

	rec := serve(srv, "GET", "/api/projects/slopfmt/merge-plan?into=slopfix", nil)
	require.Equal(t, 200, rec.Code)

	var plan map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &plan))
	assert.Equal(t, true, plan["applicable"])
	assert.Equal(t, false, plan["applied"])
	assert.Len(t, plan["releases"], 2)

	// The preview must not have moved anything.
	still, err := d.ListReleases(context.Background(), src.ID)
	require.NoError(t, err)
	assert.Len(t, still, 2)
}

func TestAdminMerge_AppliesAndSnapshots(t *testing.T) {
	t.Serial()
	srv, d, dir := newMergeServer(t)
	ctx := context.Background()

	src := mergeTestProject(t, d, "slopfmt", "wow-look-at-my/slopfmt", mergeTestRepoID)
	dst := mergeTestProject(t, d, "slopfix", "wow-look-at-my/slopfix", mergeTestRepoID)
	mergeTestReleases(t, d, src.ID, "1", "2")
	mergeTestReleases(t, d, dst.ID, "1")

	rec := serve(srv, "POST", "/api/projects/slopfmt/merge", bytes.NewBufferString(`{"into":"slopfix"}`))
	require.Equal(t, 200, rec.Code, rec.Body.String())

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, true, out["applied"])

	// The snapshot is what makes this recoverable, so its existence is asserted.
	snap, _ := out["snapshot"].(string)
	require.NotEmpty(t, snap)
	assert.Equal(t, dir, filepath.Dir(snap))
	info, err := os.Stat(snap)
	require.NoError(t, err, "the pre-merge snapshot must exist on disk")
	assert.Greater(t, info.Size(), int64(0))

	surviving, err := d.ListReleases(ctx, dst.ID)
	require.NoError(t, err)
	assert.Len(t, surviving, 3)

	resolved, aliased, err := d.ResolveProject(ctx, "slopfmt")
	require.NoError(t, err)
	assert.True(t, aliased)
	assert.Equal(t, "slopfix", resolved.Name)
}

// A blocked merge must report the reason and leave both projects intact.
func TestAdminMerge_RefusesUnrelatedProjects(t *testing.T) {
	t.Serial()
	srv, d, _ := newMergeServer(t)

	mergeTestProject(t, d, "alpha", "wow-look-at-my/alpha", "111")
	mergeTestProject(t, d, "beta", "wow-look-at-my/beta", "222")

	rec := serve(srv, "POST", "/api/projects/alpha/merge", bytes.NewBufferString(`{"into":"beta"}`))
	require.Equal(t, 409, rec.Code)

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, false, out["applicable"])
	assert.NotEmpty(t, out["conflicts"])

	_, err := d.GetProject(context.Background(), "alpha")
	require.NoError(t, err, "a refused merge leaves the source in place")
}

func TestAdminMerge_RequiresATarget(t *testing.T) {
	t.Serial()
	srv, d, _ := newMergeServer(t)
	mergeTestProject(t, d, "alpha", "wow-look-at-my/alpha", "111")

	rec := serve(srv, "POST", "/api/projects/alpha/merge", bytes.NewBufferString(`{}`))
	assert.Equal(t, 400, rec.Code)
}
