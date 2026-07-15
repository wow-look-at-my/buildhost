package repackage

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func renderFormula(t *testing.T, f BrewFormula) string {
	t.Helper()
	out, err := RenderBrewFormula(f)
	require.NoError(t, err)
	b, err := io.ReadAll(out.Reader)
	require.NoError(t, err)
	return string(b)
}

func baseFormula(resources ...BrewResource) BrewFormula {
	return BrewFormula{
		ClassName:   "Mytool",
		Name:        "mytool",
		Description: "desc",
		Homepage:    "https://example.com",
		Version:     "1.0.0",
		License:     "MIT",
		Kind:        "binary",
		Resources:   resources,
	}
}

// Homebrew can only import a formula whose STABLE spec has a URL on the
// platform doing the import; a formula carrying only on_<os> stanzas for the
// OTHER platform raises "formula requires at least a URL" and poisons the
// whole tap. Every formula therefore emits a top-level url/sha256, and a
// single-OS formula declares depends_on so a foreign-platform install fails
// cleanly instead of fetching a binary that cannot run.
func TestRenderBrewFormula_LinuxOnly(t *testing.T) {
	body := renderFormula(t, baseFormula(
		BrewResource{OS: "linux", Arch: "intel", URL: "https://dl.example/linux-amd64", SHA256: strings.Repeat("aa", 32)},
	))

	assert.Contains(t, body, "\n  url \"https://dl.example/linux-amd64\"\n  sha256 \""+strings.Repeat("aa", 32)+"\"\n")
	assert.Contains(t, body, "\n  depends_on :linux\n")
	assert.NotContains(t, body, "depends_on :macos")
	assert.Contains(t, body, "on_linux do")
}

func TestRenderBrewFormula_MacOnly(t *testing.T) {
	body := renderFormula(t, baseFormula(
		BrewResource{OS: "macos", Arch: "arm", URL: "https://dl.example/darwin-arm64", SHA256: strings.Repeat("bb", 32)},
	))

	assert.Contains(t, body, "\n  url \"https://dl.example/darwin-arm64\"\n")
	assert.Contains(t, body, "\n  depends_on :macos\n")
	assert.NotContains(t, body, "depends_on :linux")
}

func TestRenderBrewFormula_DualOS(t *testing.T) {
	linux := BrewResource{OS: "linux", Arch: "intel", URL: "https://dl.example/linux-amd64", SHA256: strings.Repeat("cc", 32)}
	mac := BrewResource{OS: "macos", Arch: "arm", URL: "https://dl.example/darwin-arm64", SHA256: strings.Repeat("dd", 32)}
	body := renderFormula(t, baseFormula(mac, linux))

	// No platform gate, on_* blocks intact, and the top-level url is the
	// canonical resource (linux/intel preferred).
	assert.NotContains(t, body, "depends_on")
	assert.Contains(t, body, "on_macos do")
	assert.Contains(t, body, "on_linux do")
	assert.Contains(t, body, "\n  url \"https://dl.example/linux-amd64\"\n  sha256 \""+strings.Repeat("cc", 32)+"\"\n")
}

func TestRenderBrewFormula_PrivateTopLevelURLUsesStrategy(t *testing.T) {
	f := baseFormula(BrewResource{OS: "linux", Arch: "intel", URL: "https://dl.example/x", SHA256: strings.Repeat("ee", 32)})
	f.Private = true
	body := renderFormula(t, f)

	assert.Contains(t, body, "\n  url \"https://dl.example/x\", using: BuildhostCurlDownloadStrategy\n")
	assert.Contains(t, body, `require_relative "../lib/buildhost_private_download"`)
}

func TestRenderBrewFormula_NoResourcesErrors(t *testing.T) {
	_, err := RenderBrewFormula(baseFormula())
	require.Error(t, err)
}

func TestBrewCanonicalResource(t *testing.T) {
	linuxIntel := BrewResource{OS: "linux", Arch: "intel", URL: "li"}
	linuxArm := BrewResource{OS: "linux", Arch: "arm", URL: "la"}
	macIntel := BrewResource{OS: "macos", Arch: "intel", URL: "mi"}
	macArm := BrewResource{OS: "macos", Arch: "arm", URL: "ma"}

	// linux/intel wins whenever present, regardless of input order.
	assert.Equal(t, "li", brewCanonicalResource([]BrewResource{macArm, linuxIntel, linuxArm}).URL)
	assert.Equal(t, "li", brewCanonicalResource([]BrewResource{linuxIntel}).URL)
	// Otherwise the first in stable (OS, Arch) order, independent of input order.
	assert.Equal(t, "la", brewCanonicalResource([]BrewResource{macArm, macIntel, linuxArm}).URL)
	assert.Equal(t, "ma", brewCanonicalResource([]BrewResource{macIntel, macArm}).URL)

	assert.Equal(t, "linux", brewDependsOnOS([]BrewResource{linuxIntel, linuxArm}))
	assert.Equal(t, "macos", brewDependsOnOS([]BrewResource{macArm}))
	assert.Equal(t, "", brewDependsOnOS([]BrewResource{linuxIntel, macArm}))
}
