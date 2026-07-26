package strip

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// Non-ELF inputs are refused by the magic check, before strip/objcopy run at
// all -- so the behavior no longer depends on whether BFD happens to reject the
// format. The cases below are exactly the ones that mattered in production.
func TestStrip_NonELFFile(t *testing.T) {
	cases := map[string][]byte{
		// BFD rejects these outright; the old code relied on that happening.
		"plain text":         []byte("this is not an ELF binary"),
		"Mach-O header":      {0xcf, 0xfa, 0xed, 0xfe, 0x0c, 0x00, 0x00, 0x01},
		"shell script":       []byte("#!/bin/sh\necho hi\n"),
		"shorter than magic": []byte("MZ"),
		// The case that actually shipped broken: BFD ACCEPTS a PE32+, so
		// strip/objcopy returned success and rewrote the file. See
		// buildPEFixture -- a Cosmopolitan APE is a PE32+ to BFD.
		"PE32+ (what an APE looks like to BFD)": buildPEFixture(t),
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "notelf")
			require.NoError(t, os.WriteFile(input, content, 0o644))

			_, err := Strip(input)
			require.ErrorIs(t, err, ErrNotELF)

			// The input must be left exactly as it was: the download path
			// falls back to serving these bytes verbatim.
			after, readErr := os.ReadFile(input)
			require.NoError(t, readErr)
			assert.Equal(t, content, after, "Strip must not modify a non-ELF input")

			// No temp files left behind.
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				assert.False(t, strings.HasPrefix(e.Name(), "stripped-"), "stripped temp should be cleaned up")
				assert.False(t, strings.HasPrefix(e.Name(), "debug-"), "debug temp should be cleaned up")
			}
		})
	}
}

// buildPEFixture returns a real PE32+ binary: the file format BFD accepts but
// this package must refuse. A hand-written MZ header is not enough -- BFD
// rejects a malformed one, so a fake fixture cannot fail this test even with
// the magic check removed. A Cosmopolitan APE binary is a well-formed PE32+,
// which is why the real go-toolchain artifact was silently rewritten.
func buildPEFixture(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0o644))

	out := filepath.Join(dir, "fixture.exe")
	cmd := exec.Command("go", "build", "-o", out, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=windows", "GOARCH=amd64")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build a PE fixture (no go toolchain?): %s: %s", err, b)
	}
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	return data
}

// The download path (static raw fmt, repackage.OpenArtifactStream) treats a
// Strip error as "serve the artifact unstripped", so the stream entry points
// must surface the refusal rather than emitting mangled bytes.
func TestStripReader_NonELFRefused(t *testing.T) {
	pe := buildPEFixture(t)

	_, _, err := StripReader(bytes.NewReader(pe), t.TempDir())
	require.ErrorIs(t, err, ErrNotELF)

	_, _, err = StripReaderDebug(bytes.NewReader(pe), t.TempDir())
	require.ErrorIs(t, err, ErrNotELF)
}

func TestStripBytes_NonELF(t *testing.T) {
	if !Available() {
		t.Skip("strip/objcopy not available")
	}
	_, err := StripBytes([]byte("not an ELF"))
	require.Error(t, err)
}

func TestStripBytes_RealBinary(t *testing.T) {
	if !Available() {
		t.Skip("strip/objcopy not available")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0o644))

	bin := filepath.Join(dir, "testbin")
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("failed to build test binary: %s: %s", err, out)
	}

	data, err := os.ReadFile(bin)
	require.NoError(t, err)

	result, err := StripBytes(data)
	require.NoError(t, err)
	assert.Less(t, len(result.Stripped), len(data))
	assert.NotEmpty(t, result.Debug)
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

	assert.Contains(t, result.StrippedPath, "stripped-")
	assert.Contains(t, result.DebugPath, "debug-")
	assert.Equal(t, dir, filepath.Dir(result.StrippedPath))
	assert.Equal(t, dir, filepath.Dir(result.DebugPath))

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
