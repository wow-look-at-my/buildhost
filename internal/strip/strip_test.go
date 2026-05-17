package strip

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestAvailable(t *testing.T) {
	// Available returns true only when both strip and objcopy are in PATH.
	_, err1 := exec.LookPath("strip")
	_, err2 := exec.LookPath("objcopy")
	expected := err1 == nil && err2 == nil

	assert.Equal(t, expected, Available())
}

func TestAvailable_MissingTools(t *testing.T) {
	// Override PATH to ensure tools are not found.
	t.Setenv("PATH", t.TempDir())

	assert.False(t, Available())
}

func TestStrip_NonexistentFile(t *testing.T) {
	_, err := Strip("/nonexistent/file/path")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read input")
}

func TestStrip_NonELFFile(t *testing.T) {
	if !Available() {
		t.Skip("strip/objcopy not available")
	}

	// Create a file with non-ELF content.
	dir := t.TempDir()
	input := filepath.Join(dir, "notelf")
	require.NoError(t, os.WriteFile(input, []byte("this is not an ELF binary"), 0o644))

	// objcopy should fail on a non-ELF file.
	_, err := Strip(input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "extract debug info")

	// Verify cleanup happened.
	_, statErr := os.Stat(input + ".stripped")
	assert.True(t, os.IsNotExist(statErr), "stripped file should be cleaned up on error")
}

func TestStrip_RealELFBinary(t *testing.T) {
	if !Available() {
		t.Skip("strip/objcopy not available")
	}

	// Build a tiny Go program to get a real ELF binary.
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0o644))

	outputBin := filepath.Join(dir, "testbin")
	cmd := exec.Command("go", "build", "-o", outputBin, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("failed to build test binary (may not have go in PATH): %s: %s", err, out)
	}

	result, err := Strip(outputBin)
	require.NoError(t, err)

	assert.Equal(t, outputBin+".stripped", result.StrippedPath)
	assert.Equal(t, outputBin+".debug", result.DebugPath)

	// Both output files should exist.
	_, err = os.Stat(result.StrippedPath)
	assert.NoError(t, err, "stripped file should exist")

	_, err = os.Stat(result.DebugPath)
	assert.NoError(t, err, "debug file should exist")

	// Stripped binary should be smaller than original.
	origInfo, _ := os.Stat(outputBin)
	strippedInfo, _ := os.Stat(result.StrippedPath)
	assert.Less(t, strippedInfo.Size(), origInfo.Size(), "stripped binary should be smaller")
}
