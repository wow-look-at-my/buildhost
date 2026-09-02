package repackage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// fakeBusybox is the binary the fake image ships; a real one is never run.
const fakeBusybox = "#!/bin/sh\necho fake busybox\n"

// fakeShellRegistry serves one image shaped like busybox:musl: a single gzip
// layer holding bin/[ as the one regular file, with bin/busybox, bin/sh and
// bin/tr hard-linked to it. requests counts every hit.
type fakeShellRegistry struct {
	server   *httptest.Server
	digest   string
	requests atomic.Int64
}

func newFakeShellRegistry(t *testing.T) *fakeShellRegistry {
	t.Helper()
	var layer bytes.Buffer
	gz := gzip.NewWriter(&layer)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "bin/", Typeflag: tar.TypeDir, Mode: 0o755}))
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "bin/[", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(fakeBusybox))}))
	_, err := tw.Write([]byte(fakeBusybox))
	require.NoError(t, err)
	for _, name := range []string{"bin/busybox", "bin/sh", "bin/tr"} {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeLink, Linkname: "bin/[", Mode: 0o755}))
	}
	// A file outside bin/ is not an applet.
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "etc/passwd", Typeflag: tar.TypeReg, Mode: 0o644, Size: 0}))
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	layerDigest := sha256Digest(layer.Bytes())

	manifest, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"layers": []map[string]any{{
			"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
			"digest":    layerDigest,
			"size":      layer.Len(),
		}},
	})
	require.NoError(t, err)

	f := &fakeShellRegistry{digest: sha256Digest(manifest)}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		json.NewEncoder(w).Encode(map[string]string{"token": "fake-token"})
	})
	mux.HandleFunc("/v2/library/busybox/manifests/"+f.digest, func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer fake-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Write(manifest)
	})
	mux.HandleFunc("/v2/library/busybox/blobs/"+layerDigest, func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		w.Write(layer.Bytes())
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeShellRegistry) cache(t *testing.T, dir string) *ShellCache {
	t.Helper()
	return &ShellCache{
		Dir:      dir,
		Registry: f.server.URL,
		TokenURL: f.server.URL + "/token",
		Images:   map[db.Arch]string{db.ArchAMD64: f.digest},
	}
}

// readShellLayer decompresses a shell layer and indexes its entries by name.
func readShellLayer(t *testing.T, compressed []byte) map[string]*tar.Header {
	t.Helper()
	zr, err := zstd.NewReader(bytes.NewReader(compressed))
	require.NoError(t, err)
	defer zr.Close()
	tr := tar.NewReader(zr)
	entries := map[string]*tar.Header{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		entries[hdr.Name] = hdr
		if hdr.Name == "bin/busybox" {
			data, err := io.ReadAll(tr)
			require.NoError(t, err)
			assert.Equal(t, fakeBusybox, string(data))
		}
	}
	return entries
}

func TestShellLayerBuiltFromTheImage(t *testing.T) {
	reg := newFakeShellRegistry(t)
	c := reg.cache(t, t.TempDir())

	compressed, diffID, err := c.Layer(context.Background(), db.ArchAMD64)
	require.NoError(t, err)
	require.Len(t, diffID, 64)

	entries := readShellLayer(t, compressed)
	require.Contains(t, entries, "bin/busybox")
	assert.Equal(t, byte(tar.TypeReg), entries["bin/busybox"].Typeflag)
	assert.Equal(t, int64(0o755), entries["bin/busybox"].Mode)
	for _, applet := range []string{"bin/sh", "bin/tr", "bin/["} {
		require.Contains(t, entries, applet)
		assert.Equal(t, byte(tar.TypeSymlink), entries[applet].Typeflag)
		assert.Equal(t, "busybox", entries[applet].Linkname, applet)
	}
	assert.NotContains(t, entries, "etc/passwd")
}

// The upstream registry is asked once per data directory: a second call in
// the same process and a fresh cache over the same directory both serve from
// what the first call stored.
func TestShellLayerIsFetchedOnce(t *testing.T) {
	reg := newFakeShellRegistry(t)
	dir := t.TempDir()
	c := reg.cache(t, dir)

	first, diff1, err := c.Layer(context.Background(), db.ArchAMD64)
	require.NoError(t, err)
	fetched := reg.requests.Load()
	require.Equal(t, int64(3), fetched, "token, manifest and layer")

	again, diff2, err := c.Layer(context.Background(), db.ArchAMD64)
	require.NoError(t, err)
	assert.Equal(t, fetched, reg.requests.Load())
	assert.Equal(t, first, again)
	assert.Equal(t, diff1, diff2)

	restarted := reg.cache(t, dir)
	cold, diff3, err := restarted.Layer(context.Background(), db.ArchAMD64)
	require.NoError(t, err)
	assert.Equal(t, fetched, reg.requests.Load(), "the disk cache answered")
	assert.Equal(t, first, cold)
	assert.Equal(t, diff1, diff3)

	cached := filepath.Join(dir, "amd64", reg.digest[len("sha256:"):])
	data, err := os.ReadFile(filepath.Join(cached, "busybox"))
	require.NoError(t, err)
	assert.Equal(t, fakeBusybox, string(data))
	applets, err := os.ReadFile(filepath.Join(cached, "applets"))
	require.NoError(t, err)
	assert.Equal(t, "[\nbusybox\nsh\ntr\n", string(applets))
}

// A manifest whose bytes do not hash to the pin is refused, so a registry
// that serves something else under the pinned digest ships nothing.
func TestShellLayerRefusesAnUnpinnedManifest(t *testing.T) {
	reg := newFakeShellRegistry(t)
	c := reg.cache(t, t.TempDir())
	c.Images = map[db.Arch]string{db.ArchAMD64: "sha256:" + string(bytes.Repeat([]byte("0"), 64))}
	bogus := c.Images[db.ArchAMD64]
	// The fake only serves its own digest, so this answers 404 before the hash check.
	_, _, err := c.Layer(context.Background(), db.ArchAMD64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")

	// Serve the real manifest under the bogus pin: the hash check is what refuses it now.
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "fake-token"})
	})
	mux.HandleFunc("/v2/library/busybox/manifests/"+bogus, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"schemaVersion":2,"layers":[]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c.Registry, c.TokenURL = srv.URL, srv.URL+"/token"
	_, _, err = c.Layer(context.Background(), db.ArchAMD64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pinned")
}

func TestShellLayerUnknownArch(t *testing.T) {
	reg := newFakeShellRegistry(t)
	c := reg.cache(t, t.TempDir())
	_, _, err := c.Layer(context.Background(), db.Arch("mips"))
	require.Error(t, err)
	assert.Equal(t, int64(0), reg.requests.Load())
}
