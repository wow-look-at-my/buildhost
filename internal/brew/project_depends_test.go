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

// The authenticated tap holds a public formula and the private one it depends
// on, and the private one downloads through the token strategy.
func TestTap_AuthenticatedTapHoldsAPrivateDependency(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	ctx := context.Background()
	app, _, _ := seedBrewProject(t, d, store, "brew-pubapp", "app-binary")
	dep, _, _ := seedBrewProject(t, d, store, "brew-secret", "dep-binary")
	require.NoError(t, d.SetProjectVisibility(ctx, dep.ID, true))
	require.NoError(t, d.SetProjectDepends(ctx, app.ID, []string{"brew-secret"}))

	files, err := h.buildTapFiles(tapRequest(true))
	require.NoError(t, err)
	assert.Contains(t, string(files["Formula/brew-pubapp.rb"]), "\n  depends_on \"pazer/build/brew-secret\"\n")
	require.Contains(t, files, "Formula/brew-secret.rb", "the dependency the formula names is in the tap")
	assert.Contains(t, string(files["Formula/brew-secret.rb"]), "using: BuildhostCurlDownloadStrategy")
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
