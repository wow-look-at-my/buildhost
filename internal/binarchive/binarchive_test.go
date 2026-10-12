package binarchive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTar builds a tar stream with the given files, in the given order.
func makeTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

// archive writes files into a binpazer archive and returns the container bytes.
func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "archive-*")
	require.NoError(t, err)
	defer f.Close()
	_, err = WriteFromTar(f, tar.NewReader(bytes.NewReader(makeTar(t, files))), Limits{})
	require.NoError(t, err)
	data, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	return data
}

func TestRoundTrip(t *testing.T) {
	t.Serial()
	files := map[string]string{
		"index.html":     "<h1>hello</h1>",
		"css/site.css":   strings.Repeat("body{margin:0}", 500),
		"assets/logo.js": "console.log('hi')",
		"404.html":       "<h1>not found</h1>",
	}
	data := archive(t, files)
	assert.True(t, IsArchive(data), "a written archive must be recognizable by its magic")

	a, err := Open(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	require.Len(t, a.Entries(), len(files))

	for name, body := range files {
		r, e, err := a.OpenFile(name)
		require.NoError(t, err, "opening %s", name)
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		assert.Equal(t, body, string(got), "contents of %s", name)
		assert.Equal(t, int64(len(body)), e.Size, "recorded size of %s", name)
	}

	_, _, err = a.OpenFile("nope.html")
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestCompressionActuallyHappens pins that entries are stored compressed: the
// container of a highly repetitive site must be far smaller than its contents.
// Without this, the archive would trade a size regression for its index.
func TestCompressionActuallyHappens(t *testing.T) {
	t.Serial()
	body := strings.Repeat("the same line over and over\n", 4000)
	data := archive(t, map[string]string{"big.txt": body})
	assert.Less(t, len(data), len(body)/4,
		"archive is %d bytes for a %d-byte payload; entries are not being compressed", len(data), len(body))
}

func TestArchiveSizeVsTarGz(t *testing.T) {
	t.Serial()
	files := map[string]string{}
	for i := 0; i < 60; i++ {
		files[fmt.Sprintf("page%02d.html", i)] = fmt.Sprintf(
			"<!doctype html><html><head><title>Page %d</title><link rel=stylesheet href=/site.css></head>"+
				"<body><h1>Page %d</h1><p>%s</p></body></html>", i, i, strings.Repeat("lorem ipsum dolor sit amet. ", 40))
	}
	files["site.css"] = strings.Repeat("body{margin:0;padding:0;font-family:system-ui}\n", 200)
	files["app.js"] = strings.Repeat("function handler(){ return document.querySelector('#app'); }\n", 200)

	raw := makeTar(t, files)
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	_, err := gw.Write(raw)
	require.NoError(t, err)
	require.NoError(t, gw.Close())

	archived := archive(t, files)
	t.Logf("tar %d bytes, tar.gz %d bytes, binpazer archive %d bytes", len(raw), gzBuf.Len(), len(archived))

	assert.Less(t, len(archived), gzBuf.Len()*8,
		"archive is %d bytes vs %d for tar.gz: per-file compression is costing too much", len(archived), gzBuf.Len())
	assert.Less(t, len(archived), len(raw)/2,
		"archive is %d bytes vs %d uncompressed: it must still compress", len(archived), len(raw))
}

// TestRandomAccessReadsOnlyItsEntry is the property a .tar.gz cannot offer:
func TestRandomAccessReadsOnlyItsEntry(t *testing.T) {
	t.Serial()
	files := map[string]string{"target.txt": "the payload"}
	for i := 0; i < 50; i++ {
		// Poorly-compressible filler, so the container is genuinely large and
		files[fmt.Sprintf("filler/%02d.txt", i)] = randomText(i, 20000)
	}
	data := archive(t, files)

	counting := &countingReaderAt{ra: bytes.NewReader(data)}
	a, err := Open(counting, int64(len(data)))
	require.NoError(t, err)

	before := counting.bytes()
	r, _, err := a.OpenFile("target.txt")
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, "the payload", string(got))

	read := counting.bytes() - before
	assert.Less(t, read, int64(4*64<<10),
		"reading one small file pulled %d bytes from a %d-byte container", read, len(data))
	assert.Less(t, read, int64(len(data)),
		"a random-access read must cost less than reading the container")
}

// TestReadCostIsIndependentOfArchiveSize pins the property a .tar.gz cannot
func TestReadCostIsIndependentOfArchiveSize(t *testing.T) {
	t.Serial()
	measure := func(n int) (read, container int64) {
		files := map[string]string{"last.txt": "payload"}
		for i := 0; i < n; i++ {
			// Random bytes, so per-entry compression cannot collapse the
			files[fmt.Sprintf("f%04d.txt", i)] = randomText(i, 20000)
		}
		data := archive(t, files)
		counting := &countingReaderAt{ra: bytes.NewReader(data)}
		a, err := Open(counting, int64(len(data)))
		require.NoError(t, err)
		before := counting.bytes()
		r, _, err := a.OpenFile("last.txt")
		require.NoError(t, err)
		_, err = io.ReadAll(r)
		require.NoError(t, err)
		return counting.bytes() - before, int64(len(data))
	}

	smallRead, smallSize := measure(20)
	largeRead, largeSize := measure(400)
	require.Greater(t, largeSize, smallSize*8, "the large container must really be much larger")

	assert.Less(t, largeRead, smallRead+int64(64<<10),
		"one read cost %d bytes in a %d-byte archive vs %d in a %d-byte one: the cost is tracking the archive size",
		largeRead, largeSize, smallRead, smallSize)
	assert.Less(t, largeRead*8, largeSize,
		"reading one file out of a %d-byte archive pulled %d bytes", largeSize, largeRead)
}

// randomText produces deterministic, poorly-compressible filler.
func randomText(seed, n int) string {
	var sb strings.Builder
	sb.Grow(n)
	x := uint32(seed*2654435761 + 1)
	for i := 0; i < n; i++ {
		x = x*1664525 + 1013904223
		sb.WriteByte(byte('a' + x>>28))
	}
	return sb.String()
}

// TestWriteTarRebuildsTheOriginal pins that the archive is lossless: the
// packaging a client expects can be regenerated from the indexed form.
func TestWriteTarRebuildsTheOriginal(t *testing.T) {
	t.Serial()
	files := map[string]string{
		"a.txt":       "alpha",
		"dir/b.txt":   "beta",
		"dir/c/d.txt": "delta",
	}
	data := archive(t, files)
	a, err := Open(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)

	var out bytes.Buffer
	require.NoError(t, a.WriteTar(&out))

	got := map[string]string{}
	tr := tar.NewReader(&out)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		got[hdr.Name] = string(body)
	}
	assert.Equal(t, files, got)
}

func TestLimits(t *testing.T) {
	t.Serial()
	f, err := os.CreateTemp(t.TempDir(), "archive-*")
	require.NoError(t, err)
	defer f.Close()
	raw := makeTar(t, map[string]string{"a": "1", "b": "2", "c": "3"})

	_, err = WriteFromTar(f, tar.NewReader(bytes.NewReader(raw)), Limits{MaxEntries: 2})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than 2 files")

	f2, err := os.CreateTemp(t.TempDir(), "archive-*")
	require.NoError(t, err)
	defer f2.Close()
	_, err = WriteFromTar(f2, tar.NewReader(bytes.NewReader(raw)), Limits{MaxTotalSize: 2})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds 2 bytes")
}

// TestOpenRejectsNonArchive pins that a tar blob (what sites stored before this
// format) is refused rather than misread -- which is what lets the caller fall
// back to the old path instead of serving garbage.
func TestOpenRejectsNonArchive(t *testing.T) {
	t.Serial()
	raw := makeTar(t, map[string]string{"a.txt": "hello"})
	assert.False(t, IsArchive(raw))
	_, err := Open(bytes.NewReader(raw), int64(len(raw)))
	assert.Error(t, err)
}

// TestDirectoriesAreNotEntries: only regular files are servable, so directory
// members must not become empty entries that shadow a real path.
func TestDirectoriesAreNotEntries(t *testing.T) {
	t.Serial()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "dir/", Mode: 0o755, Typeflag: tar.TypeDir}))
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "dir/f.txt", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg}))
	_, err := tw.Write([]byte("hi"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	f, err := os.CreateTemp(t.TempDir(), "archive-*")
	require.NoError(t, err)
	defer f.Close()
	stats, err := WriteFromTar(f, tar.NewReader(bytes.NewReader(buf.Bytes())), Limits{})
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Files)

	data, err := os.ReadFile(filepath.Clean(f.Name()))
	require.NoError(t, err)
	a, err := Open(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	require.Len(t, a.Entries(), 1)
	assert.Equal(t, "dir/f.txt", a.Entries()[0].Path)
}

// countingReaderAt records how many bytes a reader actually pulls, so a test
// can assert that a read is a seek rather than a scan.
type countingReaderAt struct {
	ra io.ReaderAt
	n  int64
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.ra.ReadAt(p, off)
	c.n += int64(n)
	return n, err
}

func (c *countingReaderAt) bytes() int64 { return c.n }
