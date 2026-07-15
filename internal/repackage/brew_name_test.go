package repackage

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

// rubyConstant is what every emitted class name must satisfy, or brew dies
// with a ".rb: syntax error" while parsing the formula (reproduced against
// Homebrew 6.0.9 with `class 7zip < Formula` and `class Go1.2 < Formula`).
var rubyConstant = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)

// TestBrewClassName pins the filename->class derivation to Homebrew's own
// Formulary.class_s (measured on 6.0.9): '-', '_', '.' (and buildhost's '/')
// separate; the following character is upcased.
func TestBrewClassName(t *testing.T) {
	cases := map[string]string{
		"go-toolchain":          "GoToolchain",
		"bin-file-fmt/binpazer": "BinFileFmtBinpazer",
		"myrepo/myapp":          "MyrepoMyapp",
		"a.b-c_d":               "ABCD",
		"go1.2.3":               "Go123", // dots MUST fold: "Go1.2.3" is a Ruby syntax error
		"dotted.app":            "DottedApp",
		"snake_case":            "SnakeCase",
		"myrepo/7app":           "Myrepo7app", // digit-leading SEGMENT is fine
	}
	for name, want := range cases {
		got := BrewClassName(name)
		assert.Equal(t, want, got, "BrewClassName(%q)", name)
		assert.Regexp(t, rubyConstant, got, "BrewClassName(%q) must be a valid Ruby constant", name)
	}
}

// TestBrewEligibleProjectName: digit-leading names are structurally
// unloadable by brew (Formulary.class_s("7zip") == "7zip", not a legal Ruby
// constant, and no substitute class satisfies the loader), so they must be
// excluded from brew entirely rather than emitted as broken Ruby.
func TestBrewEligibleProjectName(t *testing.T) {
	assert.True(t, BrewEligibleProjectName("go-toolchain"))
	assert.True(t, BrewEligibleProjectName("myrepo/7app"))
	assert.True(t, BrewEligibleProjectName("dotted.app"))
	assert.False(t, BrewEligibleProjectName("7zip"))
	assert.False(t, BrewEligibleProjectName("0ad"))
	assert.False(t, BrewEligibleProjectName(""))
}
