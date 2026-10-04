package repackage

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBrewVersionedClassName_MatchesHomebrew(t *testing.T) {
	cases := []struct {
		project, version, want string
	}{
		{"go-toolchain", "1.2.3", "GoToolchainAT123"},
		{"myapp", "1.0.0", "MyappAT100"},
		{"ns/secretapp", "2.0.1", "NsSecretappAT201"},
		{"myapp", "42", "MyappAT42"},
		{"myapp", "1.0.0-rc.1", "MyappAT100Rc1"},
		{"myapp", "1.0.0-RC1", "MyappAT100Rc1"},
		{"myapp", "1.0.0+build.5", "MyappAT100xbuild5"},
		{"my_app", "3.1", "MyAppAT31"},
	}
	for _, c := range cases {
		got, ok := BrewVersionedClassName(c.project, c.version)
		assert.True(t, ok, "%s@%s", c.project, c.version)
		assert.Equal(t, c.want, got, "%s@%s", c.project, c.version)
	}
}

// Homebrew derives "MyappAT100-X" for myapp@1.0.0--x, which is not a Ruby
// constant; a version that does not start with a digit keeps its "@".
func TestBrewVersionedClassName_RejectsUnloadable(t *testing.T) {
	for _, c := range []struct{ project, version string }{
		{"myapp", "1.0.0--x"},
		{"myapp", "abc"},
		{"myapp", ""},
		{"myapp", "1.0/2"},
		{"myapp", `1.0"`},
		{"7zip", "1.0"},
	} {
		_, ok := BrewVersionedClassName(c.project, c.version)
		assert.False(t, ok, "%s@%s", c.project, c.version)
	}
}

func TestBrewVersionedFormulaName(t *testing.T) {
	assert.Equal(t, "ns-app@1.2.3", BrewVersionedFormulaName("ns/app", "1.2.3"))
	assert.Equal(t, "Formula/ns-app.rb", BrewFormulaPath("ns/app"))
}

// brew extract renames the class to <name>AT<version>, so keg_only keys on
// the class name at load time. The latest formula's class keeps the base name.
func TestRenderBrewFormula_KegOnlyWhenClassIsRenamed(t *testing.T) {
	f := baseFormula(BrewResource{OS: "linux", Arch: "intel", URL: "https://dl.example.com/mytool", SHA256: "abc"})
	body := renderFormula(t, f)
	assert.Contains(t, body, "class "+f.ClassName+" < Formula\n")
	assert.Contains(t, body, `  keg_only "it pins one release, and the unversioned formula links the same command" if name.to_s.split("::").last != "`+BrewClassName(f.Name)+`"`+"\n")

	f.ClassName = "MytoolAT100"
	assert.Contains(t, renderFormula(t, f), `!= "`+BrewClassName(f.Name)+`"`)
}

// Homebrew auto-links a keg_only :versioned_formula keg when no sibling is
// installed, and in a third-party tap it never finds the siblings to unlink
// by short ones). The unversioned formula's link then conflicts. A string
// reason is never auto-linked.
func TestRenderBrewFormula_VersionedIsNeverAutoLinked(t *testing.T) {
	f := baseFormula(BrewResource{OS: "linux", Arch: "intel", URL: "https://dl.example.com/mytool", SHA256: "abc"})
	assert.NotContains(t, renderFormula(t, f), ":versioned_formula")
}

func TestRenderBrewFormula_PrivateStrategyIsGuarded(t *testing.T) {
	f := baseFormula(BrewResource{OS: "linux", Arch: "intel", URL: "https://dl.example.com/mytool", SHA256: "abc"})
	f.Private = true
	body := renderFormula(t, f)
	assert.True(t, strings.Contains(body, "unless defined?(BuildhostCurlDownloadStrategy)\n  class BuildhostCurlDownloadStrategy < CurlDownloadStrategy\n"))
}
