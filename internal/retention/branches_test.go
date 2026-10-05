package retention

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

// testRepo is the repository the fixture projects are linked to.
const testRepo = "wow-look-at-my/proj"

// fakeBranches stands in for GitHub, so the deleted-branch rule can be driven
// with no network. A repository named in fail answers with that error; a
// repository in neither map also errors, so a test that forgets to describe one
// fails closed exactly as production does.
type fakeBranches struct {
	live  map[string][]string
	fail  map[string]error
	calls map[string]int
}

func (f *fakeBranches) LiveBranches(_ context.Context, repoPath string) ([]string, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[repoPath]++
	if err, ok := f.fail[repoPath]; ok {
		return nil, err
	}
	names, ok := f.live[repoPath]
	if !ok {
		return nil, fmt.Errorf("unknown repository %q", repoPath)
	}
	return names, nil
}

// branchesLive answers for testRepo alone, with the branches named.
func branchesLive(names ...string) *fakeBranches {
	return &fakeBranches{live: map[string][]string{testRepo: names}}
}

// A reclaim pass asks each repository once, however many branches and builds of
// it are candidates, and does not carry the answer into the next pass.
func TestBranchLookup_OncePerRepositoryPerPass(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		v := fmt.Sprintf("v%d", i)
		agedRelease(t, d, store, p.ID, v, int64(i), "gone-one", 90*24*time.Hour)
		agedRelease(t, d, store, p.ID, v+"b", int64(i)+10, "gone-two", 90*24*time.Hour)
	}
	agedRelease(t, d, store, p.ID, "live", 50, "master", 90*24*time.Hour)

	lister := branchesLive("master")
	_, err := deletedBranchEngine(d, store, lister).Plan(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 1, lister.calls[testRepo],
		"six candidate builds across two branches are still one lookup")

	_, err = deletedBranchEngine(d, store, lister).Plan(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 2, lister.calls[testRepo], "a fresh pass asks again rather than trusting the last answer")
}

// A second repository adds exactly one more lookup, so the cache is keyed on the
// repository and not on the pass or the branch.
func TestBranchLookup_OneMorePerRepository(t *testing.T) {
	t.Serial()
	d, store, p := deletedBranchSetup(t)
	ctx := context.Background()

	second := &db.Project{Name: "other", Versioning: db.VersioningAuto, GithubRepo: "wow-look-at-my/other"}
	require.NoError(t, d.CreateProject(ctx, second))
	require.NoError(t, d.SetProjectDefaultBranch(ctx, second.ID, "master"))

	agedRelease(t, d, store, p.ID, "v1", 1, "gone", 90*24*time.Hour)
	agedRelease(t, d, store, second.ID, "v1", 1, "gone", 90*24*time.Hour)

	lister := &fakeBranches{live: map[string][]string{testRepo: {"master"}, "wow-look-at-my/other": {"master"}}}
	rep, err := deletedBranchEngine(d, store, lister).Plan(ctx)
	require.NoError(t, err)

	assert.Len(t, rep.DeletedBranchReleases, 2)
	assert.Equal(t, 1, lister.calls[testRepo])
	assert.Equal(t, 1, lister.calls["wow-look-at-my/other"])
}
