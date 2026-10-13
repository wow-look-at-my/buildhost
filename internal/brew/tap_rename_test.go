package brew

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestTapFormulaNames(t *testing.T) {
	names := tapFormulaNames([]string{
		"one/tool",
		"two/a", "two/b",
		"flat", "flat/sub",
		"deep/mid/leaf",
		"p/z", "p/q/r", "p-q",
	})
	assert.Equal(t, map[string]string{
		"one/tool":      "one",
		"two/a":         "two-a",
		"two/b":         "two-b",
		"flat":          "flat",
		"flat/sub":      "flat-sub",
		"deep/mid/leaf": "deep",
		"p/z":           "p-z",
		"p/q/r":         "p-q-r",
		"p-q":           "p-q",
	}, names)
}

func TestTap_SoleNestedFormulaTakesRootName(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "myrepo/myapp", "v1-binary")
	seedBrewProject(t, d, store, "multi/a", "a-binary")
	seedBrewProject(t, d, store, "multi/b", "b-binary")

	files, err := h.buildTapFiles(tapRequest(false))
	require.NoError(t, err)

	body := string(files["Formula/myrepo.rb"])
	assert.Contains(t, body, "class Myrepo < Formula\n")
	assert.Contains(t, body, `bin.install "myapp"`)
	assert.NotContains(t, files, "Formula/myrepo-myapp.rb")
	assert.Contains(t, files, "Formula/multi-a.rb")
	assert.Contains(t, files, "Formula/multi-b.rb")

	var renames map[string]string
	require.NoError(t, json.Unmarshal(files[tapRenamesFile], &renames))
	assert.Equal(t, map[string]string{"myrepo-myapp": "myrepo"}, renames)
}

func TestTap_NoRenamesFileWithoutRenames(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "app", "v1-binary")

	files, err := h.buildTapFiles(tapRequest(false))
	require.NoError(t, err)
	assert.NotContains(t, files, tapRenamesFile)
}

// brew version-install reads past releases from the history at the formula's current path, under its current class.
func TestTapHistory_RenamedFormulaKeepsItsName(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	proj, _, _ := seedBrewProject(t, d, store, "myrepo/myapp", "v1-binary")
	addRelease(t, d, store, proj, "1.1.0", 1001000, db.LatestBranch, "v11-binary")

	_, names, err := h.buildTap(tapRequest(false))
	require.NoError(t, err)
	_, err = h.tapHistory(tapRequest(false), names)
	require.NoError(t, err)
	h.fillWG.Wait()

	history, err := h.tapHistory(tapRequest(false), names)
	require.NoError(t, err)
	require.Len(t, history, 2)
	for _, f := range history {
		assert.Equal(t, "Formula/myrepo.rb", f.path)
		assert.Contains(t, string(f.data), "class Myrepo < Formula\n")
	}
}
