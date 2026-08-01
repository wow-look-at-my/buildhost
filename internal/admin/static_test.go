package admin

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminStaticHomebrewInstructionsUseTap(t *testing.T) {
	data, err := os.ReadFile("static/app.js")
	require.NoError(t, err)

	body := string(data)
	require.Contains(t, body, "brew tap pazer/build")
	require.Contains(t, body, "brew install pazer/build/{project}")
	require.NotContains(t, body, "brew install \" + brew + \"/{project}")
	// The tap command must clone the /tap.git endpoint (a bare host 404s) and
	// include the Homebrew 6.0+ `brew trust` step, matching llms.txt / README.
	// The \n here are the literal two-char escapes in the app.js source strings.
	require.Contains(t, body, "/tap.git\\nbrew trust pazer/build\\nbrew install pazer/build/{project}")
	require.NotContains(t, body, "(svc.brew || \"\") + \"\\nbrew install")
}

func TestAdminStaticProjectsRenderAsTree(t *testing.T) {
	data, err := os.ReadFile("static/app.js")
	require.NoError(t, err)

	// Assert the markup the tree emits, not the function names: the bundler keeps
	// these module-scoped, so a name check would pin an implementation detail while
	// silently passing on a build that renders a flat list.
	body := string(data)
	require.Contains(t, body, "project-folder-row")
	require.Contains(t, body, "project-tree-row")
	require.Contains(t, body, "project-name-cell")
	require.Contains(t, body, "project-path")
}
