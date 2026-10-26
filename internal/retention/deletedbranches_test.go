package retention

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

// fakeRefs is a RefChecker over a fixed set of refs per repo.
type fakeRefs struct {
	defaults map[string]string
	present  map[string]bool // repo + ":" + name
	broken   map[string]bool // repo + ":" + name answers an error
}

func (f fakeRefs) DefaultBranch(_ context.Context, repo string) (string, error) {
	if b, ok := f.defaults[repo]; ok {
		return b, nil
	}
	return "", errors.New("GitHub answered 404 for repository " + repo)
}

func (f fakeRefs) RefPresent(_ context.Context, repo, name string) (bool, error) {
	if f.broken[repo+":"+name] {
		return false, errors.New("GitHub answered 403")
	}
	return f.present[repo+":"+name], nil
}

func linkedProject(t *testing.T, d *db.DB, name, repo, defaultBranch string) *db.Project {
	t.Helper()
	ctx := context.Background()
	p := &db.Project{Name: name, Versioning: db.VersioningAuto, GithubRepo: repo}
	require.NoError(t, d.CreateProject(ctx, p))
	require.NoError(t, d.SetProjectDefaultBranch(ctx, p.ID, defaultBranch))
	return p
}

func TestDeletedBranches_EvictsOnlyConfirmedGone(t *testing.T) {
	t.Serial()
	d, store, _ := setup(t)
	ctx := context.Background()
	app := linkedProject(t, d, "app", "o/app", "main")
	gone := linkedProject(t, d, "gone", "o/gone", "main")

	keep := map[string]string{
		"main":      putRelease(t, d, store, app.ID, "1", 1, "main", "main-1"),
		"live":      putRelease(t, d, store, app.ID, "2", 2, "live", "live-2"),
		"tag":       putRelease(t, d, store, app.ID, "3", 3, "v1.0.0", "tag-3"),
		"flaky":     putRelease(t, d, store, app.ID, "4", 4, "flaky", "flaky-4"),
		"renamed":   putRelease(t, d, store, app.ID, "5", 5, "trunk", "trunk-5"),
		"unchecked": putRelease(t, d, store, gone.ID, "1", 1, "feature", "gone-1"),
	}
	doomed := []string{
		putRelease(t, d, store, app.ID, "6", 6, "feature/x", "x-6"),
		putRelease(t, d, store, app.ID, "7", 7, "feature/x", "x-7"),
		putRelease(t, d, store, app.ID, "8", 8, "8/merge", "pr-8"),
	}

	rc := fakeRefs{
		// GitHub reports trunk as the default although buildhost still stores main.
		defaults: map[string]string{"o/app": "trunk"},
		present:  map[string]bool{"o/app:live": true, "o/app:v1.0.0": true},
		broken:   map[string]bool{"o/app:flaky": true},
	}

	ret := New(d, store, Config{RecencyGuard: time.Hour, Enforce: true})
	ret.clock = futureClock()

	plan, err := ret.PlanDeletedBranches(ctx, rc)
	require.NoError(t, err)
	assert.False(t, plan.Enforced)
	assert.Len(t, plan.DeletedBranchReleases, 3)
	assert.Equal(t, 3, plan.BlobsDeleted)
	for _, key := range doomed {
		ok, _ := store.Exists(ctx, key)
		assert.True(t, ok, "a plan deletes nothing")
	}

	rep, err := ret.RunDeletedBranches(ctx, rc)
	require.NoError(t, err)
	assert.True(t, rep.Enforced)
	assert.Len(t, rep.DeletedBranchReleases, 3)
	assert.Equal(t, 3, rep.Releases())

	states := map[string]string{}
	for _, c := range rep.BranchChecks {
		states[c.ProjectName+"@"+c.Branch] = c.State
	}
	assert.Equal(t, map[string]string{
		"app@live":      BranchPresent,
		"app@v1.0.0":    BranchPresent,
		"app@flaky":     BranchUnknown,
		"app@trunk":     BranchDefault,
		"app@feature/x": BranchDeleted,
		"app@8/merge":   BranchDeleted,
		"gone@feature":  BranchUnknown,
	}, states, "main is the stored default, so the query never lists it")

	for _, key := range doomed {
		ok, _ := store.Exists(ctx, key)
		assert.False(t, ok, "blob %s on a deleted branch must go", key)
	}
	for name, key := range keep {
		ok, _ := store.Exists(ctx, key)
		assert.True(t, ok, "%s must stay", name)
	}
}

func TestDeletedBranches_RecencyGuardKeepsNewBuilds(t *testing.T) {
	t.Serial()
	d, store, _ := setup(t)
	ctx := context.Background()
	app := linkedProject(t, d, "app", "o/app", "main")
	key := putRelease(t, d, store, app.ID, "1", 1, "feature", "new")

	ret := New(d, store, Config{RecencyGuard: time.Hour, Enforce: true})
	rep, err := ret.RunDeletedBranches(ctx, fakeRefs{defaults: map[string]string{"o/app": "main"}})
	require.NoError(t, err)
	assert.Empty(t, rep.DeletedBranchReleases)
	ok, _ := store.Exists(ctx, key)
	assert.True(t, ok)
}
