package repackage

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// apeOCIFixture is a file shaped like a Cosmopolitan APE: the MZqFpD shell
// prologue followed by a working script.
func apeOCIFixture() []byte {
	return []byte("MZqFpD='fixture'\necho ape-oci-ok\n")
}

// buildOCI runs the OCI repackage for one platform and returns the manifest
// bytes plus a lookup into the store by digest.
func buildOCI(t *testing.T, exeFormat string, os db.OS, arch db.Arch) ([]byte, func(digest string) []byte) {
	t.Helper()
	d := openTestDB(t)
	store := openTestStore(t)
	ctx := context.Background()

	payload := apeOCIFixture()
	if exeFormat == "" {
		payload = testBinary
	}
	key, size, err := store.Put(ctx, strings.NewReader(string(payload)))
	require.NoError(t, err)
	a := &db.Artifact{
		ReleaseID: 1, OS: os, Arch: arch, Kind: db.KindBinary,
		StorageKey: key, Size: size, SHA256: key, ExeFormat: exeFormat,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	input := makeInput()
	input.Artifact = *a
	input.Reader = bytes.NewReader(payload)

	out, err := (&OCI{Store: store, DB: d}).Repackage(ctx, input)
	require.NoError(t, err)
	manifest, err := io.ReadAll(out.Reader)
	require.NoError(t, err)
	require.NoError(t, out.Reader.Close())

	fetch := func(digest string) []byte {
		key := strings.TrimPrefix(digest, "sha256:")
		rc, _, err := store.Get(ctx, key)
		require.NoError(t, err)
		defer rc.Close()
		blob, err := io.ReadAll(rc)
		require.NoError(t, err)
		return blob
	}
	return manifest, fetch
}

// An APE is not a native Linux ELF, so a scratch container cannot exec it
// directly -- the synthesized image must ship a static /bin/sh and run the
// binary through it, which is the only thing that makes
// ENTRYPOINT ["/bin/sh", "/<name>"] a runnable image rather than a
// crash-looping one.
func TestOCIRepackage_APEImageIsRunnable(t *testing.T) {
	manifest, fetch := buildOCI(t, "ape", db.OSLinux, db.ArchAMD64)

	var man struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	require.NoError(t, json.Unmarshal(manifest, &man))
	require.Len(t, man.Layers, 3, "an APE image carries base + busybox shell + binary layers")

	// The middle layer is the busybox shell: /bin/busybox plus the applet
	// symlinks the APE prologue needs, including /bin/sh itself.
	shell := fetch(man.Layers[1].Digest)
	zr, err := zstd.NewReader(bytes.NewReader(shell))
	require.NoError(t, err)
	defer zr.Close()
	tr := tar.NewReader(zr)

	binSize := int64(0)
	entries := map[string]tar.Header{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		entries[hdr.Name] = *hdr
		if hdr.Name == "bin/busybox" {
			magic := make([]byte, 6)
			_, err := io.ReadFull(tr, magic)
			require.NoError(t, err)
			assert.Equal(t, "\x7fELF", string(magic[:4]), "busybox must be a native Linux ELF")
			assert.Equal(t, byte(2), magic[4], "64-bit ELF class")
			assert.Equal(t, byte(1), magic[5], "little-endian ELF data encoding")
			n, err := io.Copy(io.Discard, tr)
			require.NoError(t, err)
			binSize = n + int64(len(magic))
		}
	}

	binHdr, ok := entries["bin/busybox"]
	require.True(t, ok, "the shell layer must contain /bin/busybox")
	assert.Equal(t, byte(tar.TypeReg), binHdr.Typeflag)
	assert.Equal(t, int64(0o755), binHdr.Mode)
	assert.Greater(t, binSize, int64(100*1024), "the busybox binary is ~1MB, not a stub")

	for _, name := range apeShellApplets {
		h, ok := entries["bin/"+name]
		require.True(t, ok, "the shell layer must provide /bin/%s", name)
		assert.Equal(t, byte(tar.TypeSymlink), h.Typeflag, "bin/%s", name)
		assert.Equal(t, "busybox", h.Linkname, "bin/%s must point at the busybox binary", name)
	}

	// The config execs the APE through that shell, not directly.
	cfg := fetch(man.Config.Digest)
	var config struct {
		Rootfs struct {
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
		Config struct {
			Entrypoint []string `json:"Entrypoint"`
		} `json:"config"`
	}
	require.NoError(t, json.Unmarshal(cfg, &config))
	assert.Equal(t, []string{"/bin/sh", "/testapp"}, config.Config.Entrypoint,
		"an APE must run through the shipped shell, not direct exec")
	require.Len(t, config.Rootfs.DiffIDs, 3, "base + shell + binary")
}

// A non-APE binary is a native ELF and keeps today's lean image: direct exec,
// two layers, no shell.
func TestOCIRepackage_NonAPEImageUnchanged(t *testing.T) {
	manifest, _ := buildOCI(t, "", db.OSLinux, db.ArchAMD64)

	var man struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	require.NoError(t, json.Unmarshal(manifest, &man))
	require.Len(t, man.Layers, 2)
}

// The shell layer is built from pinned inputs in a fixed order, so repeated
// generation is byte-identical (the pull path re-hashes everything), and each
// target arch gets its own busybox, not whichever was built first.
func TestBusyboxLayerDeterministicPerArch(t *testing.T) {
	amd1, diff1, err := busyboxLayer(db.OSLinux, db.ArchAMD64)
	require.NoError(t, err)
	amd2, diff2, err := busyboxLayer(db.OSLinux, db.ArchAMD64)
	require.NoError(t, err)
	arm, diffArm, err := busyboxLayer(db.OSLinux, db.ArchARM64)
	require.NoError(t, err)

	assert.Equal(t, amd1, amd2)
	assert.Equal(t, diff1, diff2)
	assert.NotEqual(t, diff1, diffArm, "each arch embeds its own busybox binary")
	assert.NotEqual(t, amd1, arm)
}