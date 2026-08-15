package strip

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
)

// Stripping is implemented in-process, so it is available wherever buildhost
// runs -- including the distroless production image, which ships no binutils
// and where the previous shell-out implementation silently did nothing.
func TestAvailable(t *testing.T) {
	assert.True(t, Available())
}

func TestAvailable_NoExternalTools(t *testing.T) {
	// An empty PATH removes strip(1)/objcopy(1) entirely. Stripping must still
	// be available: that is the whole point of doing it natively.
	t.Setenv("PATH", t.TempDir())
	assert.True(t, Available())

	dir := t.TempDir()
	input := filepath.Join(dir, "bin")
	require.NoError(t, os.WriteFile(input, buildELFFixture(t), 0o755))

	res, err := Strip(input)
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(res.StrippedPath); os.Remove(res.DebugPath) })

	si, err := os.Stat(res.StrippedPath)
	require.NoError(t, err)
	oi, err := os.Stat(input)
	require.NoError(t, err)
	assert.Less(t, si.Size(), oi.Size(), "stripping must actually remove bytes with no binutils present")
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
	_, err := StripBytes([]byte("not an ELF"))
	require.Error(t, err)
}

func TestStripBytes_RealBinary(t *testing.T) {
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

// buildELFFixture returns a real ELF binary built for the host, so tests can
// both inspect and EXECUTE the stripped result.
func buildELFFixture(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(src,
		[]byte("package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"strip-fixture-ok\") }\n"), 0o644))

	out := filepath.Join(dir, "fixture")
	cmd := exec.Command("go", "build", "-o", out, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build an ELF fixture (no go toolchain?): %s: %s", err, b)
	}
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	if len(data) < 4 || string(data[:4]) != "\x7fELF" {
		t.Skipf("host toolchain does not produce ELF binaries")
	}
	return data
}

// The acceptance test for the whole package: a stripped binary must still RUN.
// Everything else here -- sizes, section lists -- is secondary to that. This is
// what a corrupting implementation (the BFD shell-out on a non-ELF, or a bad
// rewrite of an ELF) cannot pass.
func TestStrip_StrippedBinaryStillRuns(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "fixture")
	require.NoError(t, os.WriteFile(input, buildELFFixture(t), 0o755))

	before, err := exec.Command(input).CombinedOutput()
	require.NoError(t, err, "fixture must run before stripping")
	require.Equal(t, "strip-fixture-ok\n", string(before))

	res, err := Strip(input)
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(res.StrippedPath); os.Remove(res.DebugPath) })
	require.NoError(t, os.Chmod(res.StrippedPath, 0o755))

	after, err := exec.Command(res.StrippedPath).CombinedOutput()
	require.NoError(t, err, "stripped binary must still run: %s", after)
	assert.Equal(t, string(before), string(after), "stripping must not change behavior")
}

// Downloads are cached by strong ETag and their sha256 is baked into Homebrew
// formulas and APT indexes, so the same artifact must strip to the same bytes
// every time.
func TestStrip_Deterministic(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "fixture")
	require.NoError(t, os.WriteFile(input, buildELFFixture(t), 0o755))

	read := func() []byte {
		res, err := Strip(input)
		require.NoError(t, err)
		defer os.Remove(res.StrippedPath)
		defer os.Remove(res.DebugPath)
		b, err := os.ReadFile(res.StrippedPath)
		require.NoError(t, err)
		return b
	}

	assert.Equal(t, read(), read(), "stripping the same input twice must produce identical bytes")
}

// What gets removed and what must survive: debug and symbol-table sections go,
// everything the loader or the Go runtime reaches stays.
func TestStrip_SectionSelection(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "fixture")
	require.NoError(t, os.WriteFile(input, buildELFFixture(t), 0o755))

	res, err := Strip(input)
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(res.StrippedPath); os.Remove(res.DebugPath) })

	orig, err := elf.Open(input)
	require.NoError(t, err)
	defer orig.Close()
	stripped, err := elf.Open(res.StrippedPath)
	require.NoError(t, err)
	defer stripped.Close()

	names := set.New[string]()
	for _, s := range stripped.Sections {
		names.Add(s.Name)
	}
	assert.False(t, names.Contains(".symtab"), ".symtab must be stripped")
	assert.False(t, names.Contains(".debug_info"), ".debug_info must be stripped")

	// Every allocated section survives, at its original file offset -- program
	// headers address them directly, so moving one would break execution.
	for _, s := range orig.Sections {
		if s.Flags&elf.SHF_ALLOC == 0 {
			continue
		}
		got := stripped.Section(s.Name)
		require.NotNil(t, got, "allocated section %s must survive", s.Name)
		assert.Equal(t, s.Offset, got.Offset, "allocated section %s must not move", s.Name)
	}

	// The debug companion carries the symbols that were removed.
	dbg, err := elf.Open(res.DebugPath)
	require.NoError(t, err)
	defer dbg.Close()
	sec := dbg.Section(".debug_info")
	require.NotNil(t, sec, "debug file must carry .debug_info")
	data, err := sec.Data()
	require.NoError(t, err)
	assert.NotEmpty(t, data)
}
