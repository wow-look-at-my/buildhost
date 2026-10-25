package pack

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildELF compiles a real Linux executable to pack. A synthetic ELF is not
// enough: the packer validates the program headers and refuses a stub.
func buildELF(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(src, []byte(
		"package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"packed-fixture\") }\n"), 0o644))
	out := filepath.Join(dir, "fixture")
	cmd := exec.Command("go", "build", "-o", out, src)
	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		// A test run under go-toolchain has -mod=vendor in GOFLAGS, which a
		// standalone fixture file has no vendor directory for.
		if strings.HasPrefix(kv, "GOFLAGS=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build an ELF fixture (no go toolchain?): %s: %s", err, b)
	}
	return out
}

func TestPackShrinksAnELFAndRoundTrips(t *testing.T) {
	t.Serial()
	require.True(t, HasAsset(runtime.GOOS, runtime.GOARCH), "the build must embed a packer for this platform")
	dir := t.TempDir()
	src := buildELF(t)
	p := New(filepath.Join(dir, "tools"))

	dst := filepath.Join(t.TempDir(), "packed")
	ok, err := p.Pack(src, dst)
	require.NoError(t, err)
	require.True(t, ok, "a Go executable must be packable")

	before, err := os.Stat(src)
	require.NoError(t, err)
	after, err := os.Stat(dst)
	require.NoError(t, err)
	assert.Less(t, after.Size(), before.Size(), "a packed binary must be smaller")

	head, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(head, []byte{0x7f, 'E', 'L', 'F'}), "packing must keep the ELF container")
	assert.True(t, bytes.Contains(head, []byte("UPX!")), "the served file must be UPX-packed")

	// Downloads of an immutable URL are byte-identical.
	again := filepath.Join(t.TempDir(), "packed-again")
	ok, err = p.Pack(src, again)
	require.NoError(t, err)
	require.True(t, ok)
	a, err := os.ReadFile(dst)
	require.NoError(t, err)
	b, err := os.ReadFile(again)
	require.NoError(t, err)
	assert.Equal(t, a, b, "packing the same input twice must produce the same bytes")

	// The packer's own verification unpacks the result; prove the runtime
	// outcome directly where this host can run the fixture.
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		require.NoError(t, os.Chmod(dst, 0o755))
		out, err := exec.Command(dst).CombinedOutput()
		require.NoError(t, err)
		assert.Equal(t, "packed-fixture\n", string(out))
	}
}

func TestPackRefusesAnAPE(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	src := filepath.Join(dir, "ape")
	ape := append([]byte("MZqFpD"), bytes.Repeat([]byte{0x90}, 64<<10)...)
	require.NoError(t, os.WriteFile(src, ape, 0o755))
	dst := filepath.Join(dir, "packed")

	ok, err := New(filepath.Join(dir, "tools")).Pack(src, dst)
	assert.False(t, ok, "an APE the packer cannot prove is refused")
	assert.Error(t, err)
	_, statErr := os.Stat(dst)
	assert.True(t, os.IsNotExist(statErr), "a refused pack leaves no output to serve")
}

// A non-executable input is refused rather than corrupted.
func TestPackRefusesPlainData(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	src := filepath.Join(dir, "data")
	require.NoError(t, os.WriteFile(src, bytes.Repeat([]byte("not-executable"), 4096), 0o644))
	dst := filepath.Join(dir, "packed")

	ok, err := New(filepath.Join(dir, "tools")).Pack(src, dst)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestHasAssetMatchesTheGeneratedPlatforms(t *testing.T) {
	t.Serial()
	for _, platform := range []string{"linux-amd64", "linux-arm64"} {
		assert.FileExists(t, "upx/upx-"+platform+".zst")
	}
	assert.False(t, HasAsset("plan9", "mips"))
}

// The embedded packer is unpacked once and reused, so a burst of downloads
// does not rewrite the tool.
func TestToolIsUnpackedOnce(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	p := New(dir)
	first, err := p.Tool()
	require.NoError(t, err)
	second, err := p.Tool()
	require.NoError(t, err)
	assert.Equal(t, first, second)
	info, err := os.Stat(first)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestPackRefusesAMissingInput(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	ok, err := New(filepath.Join(dir, "tools")).Pack(filepath.Join(dir, "absent"), filepath.Join(dir, "out"))
	assert.False(t, ok)
	assert.Error(t, err)
}
