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
}

func TestAdminStaticProjectsRenderAsTree(t *testing.T) {
	data, err := os.ReadFile("static/app.js")
	require.NoError(t, err)

	body := string(data)
	require.Contains(t, body, "App.projectTreeRows")
	require.Contains(t, body, "project-folder-row")
	require.Contains(t, body, "App.projectLabel")
}
