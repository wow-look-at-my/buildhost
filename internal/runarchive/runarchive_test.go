package runarchive

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func roundTrip(t *testing.T, src, dst string, members ...string) {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, Pack(&buf, src, members))
	require.NoError(t, Unpack(&buf, dst))
}

func TestRoundTripKeepsDataAndMetadata(t *testing.T) {
	src := t.TempDir()
	long := filepath.Join(strings.Repeat("d", 120), "unicod\u00e9-\u65e5\u672c-"+strings.Repeat("n", 110)+".txt")
	require.NoError(t, os.MkdirAll(filepath.Join(src, filepath.Dir(long)), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(src, "deep", "empty"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, long), []byte("long name"), 0o644))
	big := bytes.Repeat([]byte("0123456789abcdef"), smallFile/8)
	require.NoError(t, os.WriteFile(filepath.Join(src, "big.bin"), big, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "a.bin"), []byte("shared"), 0o644))
	require.NoError(t, os.Link(filepath.Join(src, "a.bin"), filepath.Join(src, "b.bin")))
	require.NoError(t, os.WriteFile(filepath.Join(src, "ro.txt"), []byte("read-only"), 0o644))
	require.NoError(t, os.Symlink("deep", filepath.Join(src, "dir-link")))
	require.NoError(t, os.Symlink("no-such-target", filepath.Join(src, "dangling")))
	when := time.Unix(981173106, 123456700)
	for _, p := range []string{long, "big.bin", "a.bin", "ro.txt", filepath.Join("deep", "empty")} {
		require.NoError(t, os.Chtimes(filepath.Join(src, p), when, when))
	}
	require.NoError(t, os.Chmod(filepath.Join(src, "ro.txt"), 0o444))

	dst := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dst, "ro.txt"), []byte("stale"), 0o444))
	roundTrip(t, src, dst)

	read := func(p string) string {
		data, err := os.ReadFile(filepath.Join(dst, p))
		require.NoError(t, err)
		return string(data)
	}
	assert.Equal(t, "long name", read(long), "a name past 100 bytes and outside ASCII survives")
	assert.Equal(t, string(big), read("big.bin"), "a file past the worker size streams intact")
	assert.Equal(t, "read-only", read("ro.txt"), "a read-only file in the way is replaced")
	for _, p := range []string{long, "big.bin", filepath.Join("deep", "empty")} {
		info, err := os.Stat(filepath.Join(dst, p))
		require.NoError(t, err)
		assert.True(t, info.ModTime().Equal(when), "%s keeps its 100ns mtime, got %v", p, info.ModTime())
	}
	ro, err := os.Stat(filepath.Join(dst, "ro.txt"))
	require.NoError(t, err)
	assert.Zero(t, ro.Mode().Perm()&0o200, "the replacement is read-only again")
	a, err := os.Stat(filepath.Join(dst, "a.bin"))
	require.NoError(t, err)
	b, err := os.Stat(filepath.Join(dst, "b.bin"))
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.True(t, os.SameFile(a, b), "a hardlink stays one inode")
	}
	target, err := os.Readlink(filepath.Join(dst, "dangling"))
	require.NoError(t, err)
	assert.Equal(t, "no-such-target", target, "a dangling symlink is kept")
	link, err := os.Stat(filepath.Join(dst, "dir-link"))
	require.NoError(t, err)
	assert.True(t, link.IsDir(), "a directory symlink resolves to the directory")
}

func TestRestoreIntoTheFilesystemRoot(t *testing.T) {
	// A cache path outside the workspace roots the entry at the filesystem root, a drive root on Windows.
	file := filepath.Join(t.TempDir(), "rooted", "entry.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
	require.NoError(t, os.WriteFile(file, []byte("rooted"), 0o644))
	root := filepath.VolumeName(file) + string(filepath.Separator)
	member, err := filepath.Rel(root, file)
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, Pack(&buf, root, []string{filepath.ToSlash(member)}))
	require.NoError(t, os.RemoveAll(filepath.Dir(file)))
	require.NoError(t, Unpack(&buf, root))
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, "rooted", string(data))
}

func TestEscapingEntryIsRefused(t *testing.T) {
	for _, name := range []string{"../f", "a/../../f", "/etc/f", "C:/f"} {
		var raw bytes.Buffer
		tw := tar.NewWriter(&raw)
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: 1, Mode: 0o644}))
		_, err := tw.Write([]byte("x"))
		require.NoError(t, err)
		require.NoError(t, tw.Close())
		var compressed bytes.Buffer
		zw, err := zstd.NewWriter(&compressed)
		require.NoError(t, err)
		_, err = io.Copy(zw, &raw)
		require.NoError(t, err)
		require.NoError(t, zw.Close())
		dst := t.TempDir()
		err = Unpack(&compressed, filepath.Join(dst, "in"))
		assert.ErrorContains(t, err, "leaves the directory", "entry %q", name)
		_, statErr := os.Stat(filepath.Join(dst, "f"))
		assert.True(t, os.IsNotExist(statErr), "nothing lands outside the directory for %q", name)
	}
}

func TestReadsAHostTarArchive(t *testing.T) {
	// A cache entry an older action saved was written by the host tar, GNU long names included.
	tarBin, err := exec.LookPath("tar")
	require.Nil(t, err)

	src := t.TempDir()
	longName := strings.Repeat("g", 150) + ".txt"
	require.NoError(t, os.WriteFile(filepath.Join(src, longName), []byte("gnu long name"), 0o644))
	var raw bytes.Buffer
	cmd := exec.Command(tarBin, "-cf", "-", "-C", src, ".")
	cmd.Stdout = &raw
	require.NoError(t, cmd.Run())
	raw.Write(make([]byte, 1<<20))
	var compressed bytes.Buffer
	zw, err := zstd.NewWriter(&compressed)
	require.NoError(t, err)
	_, err = io.Copy(zw, &raw)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	dst := t.TempDir()
	require.NoError(t, Unpack(&compressed, dst), "bytes after the end marker are no failure")
	data, err := os.ReadFile(filepath.Join(dst, longName))
	require.NoError(t, err)
	assert.Equal(t, "gnu long name", string(data))
}

func TestNTEntriesCarryPosixModes(t *testing.T) {
	// A tree packed on NT must extract traversable on a posix host: NT reports directory bits without the exec bit.
	t.Serial()
	defer func(prev func() string) { hostOS = prev }(hostOS)
	hostOS = func() string { return "windows" }
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "dir"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(src, "dir", "rw.txt"), []byte("rw"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(src, "dir", "ro.txt"), []byte("ro"), 0o400))
	var buf bytes.Buffer
	require.NoError(t, Pack(&buf, src, nil))
	zr, err := zstd.NewReader(&buf)
	require.NoError(t, err)
	defer zr.Close()
	modes := map[string]int64{}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		modes[hdr.Name] = hdr.Mode
	}
	assert.Equal(t, int64(0o755), modes["dir/"])
	assert.Equal(t, int64(0o644), modes["dir/rw.txt"])
	assert.Equal(t, int64(0o444), modes["dir/ro.txt"])
}

func TestNTBackslashTargetGetsItsParents(t *testing.T) {
	// node hands an NT path to the CLI with backslashes. The target and its missing parents must still be made.
	t.Serial()
	defer func(prev func() string) { hostOS = prev }(hostOS)
	hostOS = func() string { return "windows" }
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "f"), []byte("nested"), 0o644))
	var buf bytes.Buffer
	require.NoError(t, Pack(&buf, src, nil))
	base := t.TempDir()
	require.NoError(t, Unpack(&buf, base+`\build\bin`))
	data, err := os.ReadFile(filepath.Join(base, "build", "bin", "f"))
	require.NoError(t, err)
	assert.Equal(t, "nested", string(data))
}

func TestMembersPackOnlyTheirPaths(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "keep", "sub"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(src, "skip"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "keep", "sub", "f"), []byte("kept"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "skip", "f"), []byte("skipped"), 0o644))
	dst := t.TempDir()
	roundTrip(t, src, dst, "keep/sub")
	data, err := os.ReadFile(filepath.Join(dst, "keep", "sub", "f"))
	require.NoError(t, err)
	assert.Equal(t, "kept", string(data))
	_, err = os.Stat(filepath.Join(dst, "skip"))
	assert.True(t, os.IsNotExist(err), "a path outside the members is not packed")
}

func manyFiles(b *testing.B, count, size int) string {
	b.Helper()
	dir := b.TempDir()
	payload := bytes.Repeat([]byte("x"), size)
	for d := range count / 100 {
		sub := filepath.Join(dir, "d"+strings.Repeat("0", 3)+string(rune('a'+d%26))+string(rune('a'+d/26%26)))
		require.NoError(b, os.MkdirAll(sub, 0o755))
		for f := range 100 {
			require.NoError(b, os.WriteFile(filepath.Join(sub, "f"+string(rune('a'+f%26))+string(rune('a'+f/26))), payload, 0o644))
		}
	}
	return dir
}

func BenchmarkPackSmallFiles(b *testing.B) {
	dir := manyFiles(b, 5000, 1024)
	b.ResetTimer()
	for b.Loop() {
		require.NoError(b, Pack(io.Discard, dir, nil))
	}
}

func BenchmarkUnpackSmallFiles(b *testing.B) {
	dir := manyFiles(b, 5000, 1024)
	var buf bytes.Buffer
	require.NoError(b, Pack(&buf, dir, nil))
	archive := buf.Bytes()
	b.ResetTimer()
	for b.Loop() {
		b.StopTimer()
		dst := b.TempDir()
		b.StartTimer()
		require.NoError(b, Unpack(bytes.NewReader(archive), dst))
	}
}
