package ociclient

import (
	"archive/tar"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tarLayout packs a layout dir into a tarball the way docker save does,
// optionally adding hostile extra entries.
func tarLayout(t *testing.T, dir string, extra map[string][]byte) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "image.tar")
	f, err := os.Create(out)
	require.NoError(t, err)
	tw := tar.NewWriter(f)

	require.NoError(t, filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		require.NoError(t, err)
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		require.NoError(t, err)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: rel, Mode: 0o644, Size: int64(len(data))}))
		_, err = tw.Write(data)
		return err
	}))
	for name, data := range extra {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}))
		_, err = tw.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, f.Close())
	return out
}

func TestOpenLayout_Dir(t *testing.T) {
	dir, manifestDigest := buildImageLayout(t, []byte("layer-bytes"))
	l, err := openLayout(dir)
	require.NoError(t, err)
	defer l.Close()

	root, err := l.root()
	require.NoError(t, err)
	assert.Equal(t, manifestDigest, root.Digest)

	_, m, err := l.readManifest(root)
	require.NoError(t, err)
	assert.Len(t, m.Layers, 1)
}

func TestOpenLayout_Tarball(t *testing.T) {
	dir, manifestDigest := buildImageLayout(t, []byte("layer-bytes"))
	// docker save adds legacy members; hostile names must be ignored, not extracted.
	tarball := tarLayout(t, dir, map[string][]byte{
		"manifest.json":         []byte(`[]`),
		"repositories":          []byte(`{}`),
		"../escape-attempt":     []byte("nope"),
		"blobs/sha256/notahash": []byte("nope"),
	})

	l, err := openLayout(tarball)
	require.NoError(t, err)
	defer l.Close()

	root, err := l.root()
	require.NoError(t, err)
	assert.Equal(t, manifestDigest, root.Digest)

	body, m, err := l.readManifest(root)
	require.NoError(t, err)
	assert.NotEmpty(t, body)
	require.Len(t, m.Layers, 1)
	_, err = l.blobPath(m.Layers[0].Digest, m.Layers[0].Size)
	assert.NoError(t, err)

	// The hostile entries were not extracted anywhere.
	assert.NoFileExists(t, filepath.Join(l.dir, "..", "escape-attempt"))
	assert.NoFileExists(t, filepath.Join(l.dir, "blobs", "sha256", "notahash"))
}

func TestOpenLayout_TarballCleanup(t *testing.T) {
	dir, _ := buildImageLayout(t, []byte("layer-bytes"))
	tarball := tarLayout(t, dir, nil)
	l, err := openLayout(tarball)
	require.NoError(t, err)
	extracted := l.dir
	require.DirExists(t, extracted)
	l.Close()
	assert.NoDirExists(t, extracted, "Close must remove the extraction dir")
}

func TestOpenLayout_Errors(t *testing.T) {
	_, err := openLayout(filepath.Join(t.TempDir(), "missing"))
	assert.Error(t, err)

	// Directory without index.json.
	_, err = openLayout(t.TempDir())
	assert.ErrorContains(t, err, "index.json")

	// Tarball without index.json.
	empty := tarLayout(t, t.TempDir(), map[string][]byte{"random.txt": []byte("x")})
	_, err = openLayout(empty)
	assert.ErrorContains(t, err, "index.json")
}

func TestLayoutRoot_RejectsMultiImage(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.json"),
		[]byte(`{"manifests":[{"digest":"sha256:aa"},{"digest":"sha256:bb"}]}`), 0o644))
	l := &layout{dir: dir}
	_, err := l.root()
	assert.ErrorContains(t, err, "exactly 1")
}

func TestBlobPath_Validation(t *testing.T) {
	dir, _ := buildImageLayout(t, []byte("layer-bytes"))
	l := &layout{dir: dir}

	_, err := l.blobPath("sha512:deadbeef", -1)
	assert.ErrorContains(t, err, "only sha256")

	_, err = l.blobPath("sha256:"+testHex64(), -1)
	assert.ErrorContains(t, err, "missing")

	d := writeBlob(t, dir, []byte("abc"))
	_, err = l.blobPath(d, 999)
	assert.ErrorContains(t, err, "descriptor says")

	p, err := l.blobPath(d, 3)
	require.NoError(t, err)
	assert.FileExists(t, p)
}

func testHex64() string {
	s := ""
	for range 64 {
		s += "a"
	}
	return s
}
