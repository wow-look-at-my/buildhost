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

	body := string(data)
	require.Contains(t, body, "App.projectTreeRows")
	require.Contains(t, body, "project-folder-row")
	require.Contains(t, body, "App.projectLabel")
}
