package brew

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTap_FormulaDependsOnItsDependency(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	app, _, _ := seedBrewProject(t, d, store, "api-cli", "app-binary")
	seedBrewProject(t, d, store, "ascii-timeline", "dep-binary")
	require.NoError(t, d.SetProjectDepends(context.Background(), app.ID, []string{"ascii-timeline"}))

	files, err := h.buildTapFiles(tapRequest(false))
	require.NoError(t, err)
	assert.Contains(t, string(files["Formula/api-cli.rb"]), "\n  depends_on \"pazer/build/ascii-timeline\"\n")
	assert.NotContains(t, string(files["Formula/ascii-timeline.rb"]), `depends_on "`)
}

// A dependency the tap names apart from its folded name is depended on by its tap name.
func TestTap_DependsOnRenamedFormula(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	app, _, _ := seedBrewProject(t, d, store, "app", "app-binary")
	seedBrewProject(t, d, store, "myrepo/myapp", "dep-binary")
	require.NoError(t, d.SetProjectDepends(context.Background(), app.ID, []string{"myrepo/myapp"}))

	files, err := h.buildTapFiles(tapRequest(false))
	require.NoError(t, err)
	assert.Contains(t, string(files["Formula/app.rb"]), "depends_on \"pazer/build/myrepo\"\n")
}
