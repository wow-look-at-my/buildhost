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
	t.Serial()
	body := renderFormula(t, baseFormula(
		BrewResource{OS: "linux", Arch: "intel", URL: "https://dl.example/linux-amd64", SHA256: strings.Repeat("aa", 32)},
	))

	assert.Contains(t, body, "\n  url \"https://dl.example/linux-amd64\"\n  sha256 \""+strings.Repeat("aa", 32)+"\"\n")
	assert.Contains(t, body, "\n  depends_on :linux\n")
	assert.NotContains(t, body, "depends_on :macos")
	assert.Contains(t, body, "on_linux do")
}

func TestRenderBrewFormula_MacOnly(t *testing.T) {
	t.Serial()
	body := renderFormula(t, baseFormula(
		BrewResource{OS: "macos", Arch: "arm", URL: "https://dl.example/darwin-arm64", SHA256: strings.Repeat("bb", 32)},
	))

	assert.Contains(t, body, "\n  url \"https://dl.example/darwin-arm64\"\n")
	assert.Contains(t, body, "\n  depends_on :macos\n")
	assert.NotContains(t, body, "depends_on :linux")
}

func TestRenderBrewFormula_DualOS(t *testing.T) {
	t.Serial()
	linux := BrewResource{OS: "linux", Arch: "intel", URL: "https://dl.example/linux-amd64", SHA256: strings.Repeat("cc", 32)}
	mac := BrewResource{OS: "macos", Arch: "arm", URL: "https://dl.example/darwin-arm64", SHA256: strings.Repeat("dd", 32)}
	body := renderFormula(t, baseFormula(mac, linux))

	// No platform gate, on_* blocks intact, and the top-level url is the
	assert.NotContains(t, body, "depends_on")
	assert.Contains(t, body, "on_macos do")
	assert.Contains(t, body, "on_linux do")
	assert.Contains(t, body, "\n  url \"https://dl.example/linux-amd64\"\n  sha256 \""+strings.Repeat("cc", 32)+"\"\n")
}

func TestRenderBrewFormula_PrivateTopLevelURLUsesStrategy(t *testing.T) {
	t.Serial()
	f := baseFormula(BrewResource{OS: "linux", Arch: "intel", URL: "https://dl.example/x", SHA256: strings.Repeat("ee", 32)})
	f.Private = true
	body := renderFormula(t, f)

	assert.Contains(t, body, "\n  url \"https://dl.example/x\", using: BuildhostCurlDownloadStrategy\n")
	assert.Contains(t, body, `require_relative "../lib/buildhost_private_download"`)
}

func TestRenderBrewFormula_NoResourcesErrors(t *testing.T) {
	t.Serial()
	_, err := RenderBrewFormula(baseFormula())
	require.Error(t, err)
}

// The flag-OFF rendering is pinned byte-for-byte: formula bytes feed the tap's
// content-addressed git objects, so unintended drift mints a spurious tap
// commit for every project on deploy. Any deliberate change to the formula
// (e.g. the skip_clean/chmod mode fix) belongs in this golden.
func TestRenderBrewFormula_ServiceOffByteIdentical(t *testing.T) {
	t.Serial()
	body := renderFormula(t, baseFormula(
		BrewResource{OS: "linux", Arch: "intel", URL: "https://dl.example/linux-amd64", SHA256: strings.Repeat("aa", 32)},
	))

	want := `class Mytool < Formula
  desc "desc"
  homepage "https://example.com"
  version "1.0.0"
  license "MIT"

  url "https://dl.example/linux-amd64"
  sha256 "` + strings.Repeat("aa", 32) + `"
  depends_on :linux
  on_linux do
    on_intel do
      url "https://dl.example/linux-amd64"
      sha256 "` + strings.Repeat("aa", 32) + `"
    end
  end

  # Homebrew's Cleaner rewrites the mode of everything under bin: 0555 for a
  # file it recognizes as executable (shebang script, ELF, Mach-O), 0444 for
  # anything else. An Actually Portable Executable is none of those, and it
  # rewrites itself in place on first run, so it needs both bits the Cleaner
  # would take away. skip_clean keeps the 0755 installed below.
  skip_clean "bin"

  def install
    bin.install "mytool"
    chmod 0755, bin/"mytool"
  end
end
`
	assert.Equal(t, want, body)
	assert.NotContains(t, body, "service do")
}

// The opt-in service block (projects.create_service): brew services manages the
// installed binary as a login service. keep_alive uses the CRASH-ONLY form --
// `successful_exit: false` renders KeepAlive {SuccessfulExit: false} in the
// launchd plist (Homebrew service.rb KEEP_ALIVE_KEYS) -- because plain
// `keep_alive true` would respawn a deliberately-exiting app (a single-instance
func TestRenderBrewFormula_ServiceBlock(t *testing.T) {
	t.Serial()
	f := baseFormula(BrewResource{OS: "macos", Arch: "arm", URL: "https://dl.example/darwin-arm64", SHA256: strings.Repeat("bb", 32)})
	f.Service = true
	body := renderFormula(t, f)

	want := `  def install
    bin.install "mytool"
    chmod 0755, bin/"mytool"
  end

  service do
    run [opt_bin/"mytool"]
    keep_alive successful_exit: false
    log_path var/"log/mytool.log"
    error_log_path var/"log/mytool.log"
    process_type :interactive
  end
end
`
	assert.True(t, strings.HasSuffix(body, want), "formula must end with the service block:\n%s", body)
}

// The service block runs opt_bin/<InstallName>, which only a bin.install
// stages -- non-binary kinds must never emit it, flag or no flag.
func TestRenderBrewFormula_ServiceNonBinaryKindOmitsBlock(t *testing.T) {
	t.Serial()
	f := baseFormula(BrewResource{OS: "linux", Arch: "intel", URL: "https://dl.example/lib", SHA256: strings.Repeat("cc", 32)})
	f.Kind = "library"
	f.Service = true
	body := renderFormula(t, f)

	assert.NotContains(t, body, "service do")
	assert.Contains(t, body, "lib.install \"mytool\"")
}

// Slash-namespaced projects install (and therefore run) the BASENAME -- the
// service block must reference the same staged name bin.install produces.
func TestRenderBrewFormula_ServiceSlashNamespacedUsesBasename(t *testing.T) {
	t.Serial()
	f := baseFormula(BrewResource{OS: "linux", Arch: "intel", URL: "https://dl.example/x", SHA256: strings.Repeat("dd", 32)})
	f.Name = "myrepo/myapp"
	f.Service = true
	body := renderFormula(t, f)

	assert.Contains(t, body, "run [opt_bin/\"myapp\"]")
	assert.Contains(t, body, "log_path var/\"log/myapp.log\"")
}

func TestRenderBrewFormula_BinaryKeepsExecutableWritableMode(t *testing.T) {
	t.Serial()
	body := renderFormula(t, baseFormula(
		BrewResource{OS: "linux", Arch: "intel", URL: "https://dl.example/x", SHA256: strings.Repeat("aa", 32)},
	))

	assert.Contains(t, body, "\n  skip_clean \"bin\"\n")
	assert.Contains(t, body, "\n    chmod 0755, bin/\"mytool\"\n")
	// skip_clean is a class-body call, not an install step: it must sit
	assert.Less(t, strings.Index(body, `skip_clean "bin"`), strings.Index(body, "def install"))
}

// The Cleaner opt-out exists for the bin.install path; other kinds stage
// nothing under bin, so they keep Homebrew's default cleanup.
func TestRenderBrewFormula_NonBinaryKindHasNoSkipClean(t *testing.T) {
	t.Serial()
	for _, kind := range []string{"library", "archive"} {
		f := baseFormula(BrewResource{OS: "linux", Arch: "intel", URL: "https://dl.example/x", SHA256: strings.Repeat("bb", 32)})
		f.Kind = kind
		body := renderFormula(t, f)

		assert.NotContains(t, body, "skip_clean", "kind %q", kind)
		assert.NotContains(t, body, "chmod", "kind %q", kind)
	}
}

// Slash-namespaced projects install the BASENAME (brew strips the lone
// top-level directory when unpacking), so the chmod must name the same file
// bin.install staged -- chmod'ing the slashed path would ENOENT the install.
func TestRenderBrewFormula_SlashNamespacedChmodsBasename(t *testing.T) {
	t.Serial()
	f := baseFormula(BrewResource{OS: "linux", Arch: "intel", URL: "https://dl.example/x", SHA256: strings.Repeat("cc", 32)})
	f.Name = "myrepo/myapp"
	body := renderFormula(t, f)

	assert.Contains(t, body, "\n    bin.install \"myapp\"\n    chmod 0755, bin/\"myapp\"\n")
}

func TestBrewCanonicalResource(t *testing.T) {
	t.Serial()
	linuxIntel := BrewResource{OS: "linux", Arch: "intel", URL: "li"}
	linuxArm := BrewResource{OS: "linux", Arch: "arm", URL: "la"}
	macIntel := BrewResource{OS: "macos", Arch: "intel", URL: "mi"}
	macArm := BrewResource{OS: "macos", Arch: "arm", URL: "ma"}

	// linux/intel wins whenever present, regardless of input order.
	assert.Equal(t, "li", brewCanonicalResource([]BrewResource{macArm, linuxIntel, linuxArm}).URL)
	assert.Equal(t, "li", brewCanonicalResource([]BrewResource{linuxIntel}).URL)
	assert.Equal(t, "la", brewCanonicalResource([]BrewResource{macArm, macIntel, linuxArm}).URL)
	assert.Equal(t, "ma", brewCanonicalResource([]BrewResource{macIntel, macArm}).URL)

	assert.Equal(t, "linux", brewDependsOnOS([]BrewResource{linuxIntel, linuxArm}))
	assert.Equal(t, "macos", brewDependsOnOS([]BrewResource{macArm}))
	assert.Equal(t, "", brewDependsOnOS([]BrewResource{linuxIntel, macArm}))
}
