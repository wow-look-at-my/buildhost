package repackage

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestDebRepackage_ControlCarriesProjectDepends(t *testing.T) {
	t.Serial()
	input := makeInput()
	input.Project.Depends = "ascii-timeline,team/tool_x"
	input.Project.AptDepends = "bubblewrap | docker.io"

	control := debControl(t, input)
	assert.Contains(t, control, "\nDepends: ascii-timeline, team-tool-x, bubblewrap | docker.io\n",
		"a dependency is named by its deb package name, ahead of apt_depends")
}

func TestDebDependsLine_ProjectDependsOnly(t *testing.T) {
	line, err := DebDependsLine(db.Project{Name: "api-cli", Depends: "ascii-timeline"})
	require.NoError(t, err)
	assert.Equal(t, "Depends: ascii-timeline\n", line)
}

func TestRenderBrewFormula_DependsOnTapFormula(t *testing.T) {
	f := baseFormula(BrewResource{OS: "linux", Arch: "intel", URL: "https://dl/x", SHA256: "aa"})
	f.Depends = []string{"ascii-timeline", "team-lib"}

	got := renderFormula(t, f)
	assert.Contains(t, got, "\n  depends_on \"pazer/build/ascii-timeline\"\n")
	assert.Contains(t, got, "\n  depends_on \"pazer/build/team-lib\"\n")
}

func TestRenderBrewFormula_NoDependsNoLine(t *testing.T) {
	got := renderFormula(t, baseFormula(BrewResource{OS: "linux", Arch: "intel", URL: "https://dl/x", SHA256: "aa"}))
	assert.NotContains(t, got, `depends_on "`)
}

func TestBrewRepackage_DependsOnFoldedFormula(t *testing.T) {
	t.Serial()
	input := makeInput()
	input.Project.Depends = "team/lib"

	out, err := (&Brew{}).Repackage(t.Context(), input)
	require.NoError(t, err)
	body, err := io.ReadAll(out.Reader)
	require.NoError(t, err)
	assert.Contains(t, string(body), "depends_on \"pazer/build/team-lib\"")
}
