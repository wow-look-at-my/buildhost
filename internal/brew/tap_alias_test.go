package brew

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddRootAliases(t *testing.T) {
	files := map[string][]byte{
		"Formula/one-tool.rb":      nil,
		"Formula/two-a.rb":         nil,
		"Formula/two-b.rb":         nil,
		"Formula/flat.rb":          nil,
		"Formula/flat-sub.rb":      nil,
		"Formula/deep-mid-leaf.rb": nil,
	}
	addRootAliases(files, []string{"one/tool", "two/a", "two/b", "flat", "flat/sub", "deep/mid/leaf"})

	assert.Equal(t, "../Formula/one-tool.rb", string(files["Aliases/one"]))
	assert.Equal(t, "../Formula/deep-mid-leaf.rb", string(files["Aliases/deep"]))
	assert.Equal(t, "../Formula/deep-mid-leaf.rb", string(files["Aliases/deep-mid"]))
	assert.NotContains(t, files, "Aliases/two", "a root with two nested formulas names neither")
	assert.NotContains(t, files, "Aliases/flat", "a root with its own formula keeps it")
}

func TestTap_RootAliasForSoleNestedProject(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "myrepo/myapp", "v1-binary")
	seedBrewProject(t, d, store, "multi/a", "a-binary")
	seedBrewProject(t, d, store, "multi/b", "b-binary")

	files, err := h.buildTapFiles(tapRequest(false))
	require.NoError(t, err)
	assert.Equal(t, "../Formula/myrepo-myapp.rb", string(files["Aliases/myrepo"]))
	assert.NotContains(t, files, "Aliases/multi")
}

// Brew reads Aliases/<name> only as a symlink to a formula file, so the clone must carry a real link that resolves.
func TestSmartClone_RootAliasIsSymlinkToFormula(t *testing.T) {
	t.Serial()
	requireGit(t)

	oldTTL := tapCacheTTL
	tapCacheTTL = 0
	t.Cleanup(func() { tapCacheTTL = oldTTL })

	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "myrepo/myapp", "v1-binary")
	ts := smartTapServer(t, h)

	dir := filepath.Join(t.TempDir(), "tap")
	runGit(t, t.TempDir(), "clone", ts.URL+"/brew/tap.git", dir)
	runGit(t, dir, "fsck", "--strict")

	assert.Contains(t, runGit(t, dir, "ls-tree", "HEAD", "Aliases/"), "120000 blob ")
	target, err := os.Readlink(filepath.Join(dir, "Aliases", "myrepo"))
	require.NoError(t, err)
	assert.Equal(t, "../Formula/myrepo-myapp.rb", target)
	body, err := os.ReadFile(filepath.Join(dir, "Aliases", "myrepo"))
	require.NoError(t, err)
	assert.Contains(t, string(body), "class MyrepoMyapp < Formula")
}
