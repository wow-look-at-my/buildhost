package repackage

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// readLayerFiles returns the regular files of a zstd-compressed image layer,
// keyed by their path inside it.
func readLayerFiles(t *testing.T, compressed []byte) map[string][]byte {
	t.Helper()
	zr, err := zstd.NewReader(bytes.NewReader(compressed))
	require.NoError(t, err)
	defer zr.Close()
	tr := tar.NewReader(zr)
	files := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return files
		}
		require.NoError(t, err)
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		files[hdr.Name] = data
	}
}

// readLayerDirModes returns the permission bits of every directory in a layer.
func readLayerDirModes(t *testing.T, compressed []byte) map[string]int64 {
	t.Helper()
	zr, err := zstd.NewReader(bytes.NewReader(compressed))
	require.NoError(t, err)
	defer zr.Close()
	tr := tar.NewReader(zr)
	modes := map[string]int64{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return modes
		}
		require.NoError(t, err)
		if hdr.Typeflag == tar.TypeDir {
			modes[hdr.Name] = hdr.Mode
		}
	}
}

// TestOCIRepackageEssentials verifies the synthesized image carries the shared
// essentials base layer (CA certs + minimal rootfs) in addition to the binary layer:
func TestOCIRepackageEssentials(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	store := openTestStore(t)
	ctx := context.Background()

	proj := &db.Project{Name: "testapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "v1.2.3", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, rel))

	key, size, err := store.Put(ctx, strings.NewReader(string(testBinary)))
	require.NoError(t, err)
	a := &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	rp := &OCI{Store: store, DB: d}
	input := makeInput()
	input.Artifact = *a

	output, err := rp.Repackage(ctx, input)
	require.NoError(t, err)
	manifestData, err := io.ReadAll(output.Reader)
	require.NoError(t, err)

	cfgKey, _, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "oci-config")
	require.NoError(t, err)
	baseKey, _, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "oci-base-layer")
	require.NoError(t, err)
	_, _, _, _, _, err = d.GetPackagedArtifact(ctx, a.ID, "oci-layer")
	require.NoError(t, err)

	var man struct {
		Layers []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
		} `json:"layers"`
	}
	require.NoError(t, json.Unmarshal(manifestData, &man))
	require.Len(t, man.Layers, 2)
	assert.Equal(t, "sha256:"+baseKey, man.Layers[0].Digest)
	for _, l := range man.Layers {
		assert.Equal(t, "application/vnd.oci.image.layer.v1.tar+zstd", l.MediaType)
	}

	cfgRC, _, err := store.Get(ctx, cfgKey)
	require.NoError(t, err)
	cfgBytes, err := io.ReadAll(cfgRC)
	cfgRC.Close()
	require.NoError(t, err)
	var cfg struct {
		Rootfs struct {
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
		Config struct {
			Entrypoint []string `json:"Entrypoint"`
			Env        []string `json:"Env"`
			WorkingDir string   `json:"WorkingDir"`
			User       string   `json:"User"`
		} `json:"config"`
	}
	require.NoError(t, json.Unmarshal(cfgBytes, &cfg))
	require.Len(t, cfg.Rootfs.DiffIDs, 2)
	_, baseDiffID, err := essentialsLayer()
	require.NoError(t, err)
	assert.Equal(t, "sha256:"+baseDiffID, cfg.Rootfs.DiffIDs[0])
	assert.Equal(t, []string{"/testapp"}, cfg.Config.Entrypoint)
	assert.Equal(t, "/", cfg.Config.WorkingDir)
	assert.Contains(t, cfg.Config.Env, "SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt")
	assert.Empty(t, cfg.Config.User)
}

// apeBinary opens with the APE prologue; the rest is never executed.
var apeBinary = []byte("MZqFpD='\n#!/bin/sh\nexit 0\n'\n")

// An APE image carries a shell layer between the essentials and the binary
// and enters through /bin/sh, because the kernel cannot exec the APE's
// shell-script header on its own.
func TestOCIRepackageAPEGetsAShell(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	store := openTestStore(t)
	ctx := context.Background()

	proj := &db.Project{Name: "apeapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "v1.0.0", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, rel))
	key, size, err := store.Put(ctx, bytes.NewReader(apeBinary))
	require.NoError(t, err)
	a := &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	rp := &OCI{Store: store, DB: d, Shell: newFakeShellRegistry(t).cache(t, t.TempDir())}
	input := makeInput()
	input.Project = *proj
	input.Artifact = *a
	input.Reader = bytes.NewReader(apeBinary)
	input.Size = int64(len(apeBinary))

	output, err := rp.Repackage(ctx, input)
	require.NoError(t, err)
	manifestData, err := io.ReadAll(output.Reader)
	require.NoError(t, err)

	var man struct {
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	require.NoError(t, json.Unmarshal(manifestData, &man))
	require.Len(t, man.Layers, 3)
	shellKey, _, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "oci-shell-layer")
	require.NoError(t, err)
	assert.Equal(t, "sha256:"+shellKey, man.Layers[1].Digest)

	rc, _, err := store.Get(ctx, shellKey)
	require.NoError(t, err)
	shellData, err := io.ReadAll(rc)
	rc.Close()
	require.NoError(t, err)
	entries := readShellLayer(t, shellData)
	require.Contains(t, entries, "bin/sh")
	assert.Equal(t, "busybox", entries["bin/sh"].Linkname)

	cfgKey, _, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "oci-config")
	require.NoError(t, err)
	cfgRC, _, err := store.Get(ctx, cfgKey)
	require.NoError(t, err)
	cfgBytes, err := io.ReadAll(cfgRC)
	cfgRC.Close()
	require.NoError(t, err)
	var cfg struct {
		Rootfs struct {
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
		Config struct {
			Entrypoint []string `json:"Entrypoint"`
		} `json:"config"`
	}
	require.NoError(t, json.Unmarshal(cfgBytes, &cfg))
	// A rolling updater clones this string onto the replacement container.
	assert.Equal(t, []string{"/apeapp"}, cfg.Config.Entrypoint)
	require.Len(t, cfg.Rootfs.DiffIDs, 3)
	_, baseDiffID, err := essentialsLayer()
	require.NoError(t, err)
	assert.Equal(t, "sha256:"+baseDiffID, cfg.Rootfs.DiffIDs[0])

	binKey, _, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "oci-layer")
	require.NoError(t, err)
	binRC, _, err := store.Get(ctx, binKey)
	require.NoError(t, err)
	binData, err := io.ReadAll(binRC)
	binRC.Close()
	require.NoError(t, err)
	files := readLayerFiles(t, binData)

	// The APE lives out of the way, and each spelling reaches a launcher.
	assert.Equal(t, apeBinary, files["usr/local/lib/apeapp/apeapp"])
	launcher := string(apeImageLauncher("apeapp"))
	assert.Equal(t, launcher, string(files["apeapp"]))
	assert.Equal(t, launcher, string(files["usr/local/bin/apeapp"]))
	assert.True(t, strings.HasPrefix(launcher, "#!/bin/sh\n"), "a launcher the kernel can exec starts with a shebang")
	assert.Contains(t, launcher, "/usr/local/lib/apeapp/apeapp")
	// A deployment mounts a noexec tmpfs over /tmp, where the unpack writes
	// fine and the exec dies.
	assert.Contains(t, launcher, "${TMPDIR:=/var/lib/ape}",
		"the launcher must point the unpack at a directory nothing mounts over")
	assert.Contains(t, launcher, "export TMPDIR", "the APE reads TMPDIR from the environment")
}

// The launcher names /var/lib/ape, so the image has to ship it, writable.
func TestEssentialsShipsTheAPEUnpackDir(t *testing.T) {
	t.Serial()
	e, err := buildEssentials()
	require.NoError(t, err)

	modes := readLayerDirModes(t, e.compressed)
	mode, ok := modes["var/lib/ape/"]
	require.True(t, ok, "the essentials layer must create the directory the launcher unpacks into")
	assert.Equal(t, int64(0o1777), mode, "any uid the image runs as has to be able to write there")
}

// A plain ELF keeps the single-file layout: the binary at /<name>, no launcher.
func TestOCIRepackagePlainBinaryLayout(t *testing.T) {
	t.Serial()
	store := openTestStore(t)
	body := []byte("\x7fELF not an APE")
	key, size, diffID, err := ociWriteLayer(context.Background(), store, bytes.NewReader(body), int64(len(body)), "plainapp", false)
	require.NoError(t, err)
	require.NotEmpty(t, diffID)
	require.NotZero(t, size)

	rc, _, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	data, err := io.ReadAll(rc)
	rc.Close()
	require.NoError(t, err)
	files := readLayerFiles(t, data)
	assert.Equal(t, map[string][]byte{"plainapp": body}, files)
}

// Without a shell cache an APE image is refused outright: an image that
// cannot start must not be served as if it could.
func TestOCIRepackageAPEWithoutShellCacheFails(t *testing.T) {
	t.Serial()
	store := openTestStore(t)
	rp := &OCI{Store: store}
	input := makeInput()
	input.Reader = bytes.NewReader(apeBinary)
	input.Size = int64(len(apeBinary))

	_, err := rp.Repackage(context.Background(), input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shell")
}

func TestOCIRepackageNonAPENeedsNoShell(t *testing.T) {
	t.Serial()
	store := openTestStore(t)
	rp := &OCI{Store: store}
	input := makeInput()
	input.Reader = bytes.NewReader(testBinary)
	input.Size = int64(len(testBinary))

	output, err := rp.Repackage(context.Background(), input)
	require.NoError(t, err)
	manifestData, err := io.ReadAll(output.Reader)
	require.NoError(t, err)
	var man struct {
		Layers []struct{} `json:"layers"`
	}
	require.NoError(t, json.Unmarshal(manifestData, &man))
	assert.Len(t, man.Layers, 2)
}

func TestEssentialsLayerContents(t *testing.T) {
	t.Serial()
	data, diffID, err := essentialsLayer()
	require.NoError(t, err)
	require.NotEmpty(t, data)
	require.Len(t, diffID, 64) // hex-encoded sha256

	zr, err := zstd.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer zr.Close()
	tr := tar.NewReader(zr)

	headers := map[string]*tar.Header{}
	var caPEM []byte
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		headers[hdr.Name] = hdr
		if hdr.Name == "etc/ssl/certs/ca-certificates.crt" {
			caPEM, err = io.ReadAll(tr)
			require.NoError(t, err)
		}
	}

	for _, name := range []string{"etc/", "etc/passwd", "etc/group", "etc/nsswitch.conf", "etc/ssl/", "etc/ssl/certs/", "etc/ssl/certs/ca-certificates.crt", "tmp/"} {
		require.Contains(t, headers, name, "essentials layer must contain %s", name)
	}

	// The embedded CA bundle is real and parseable.
	require.NotEmpty(t, caPEM)
	require.True(t, x509.NewCertPool().AppendCertsFromPEM(caPEM))

	// /tmp is world-writable + sticky; the bit must round-trip through the tar header.
	assert.Equal(t, byte(tar.TypeDir), headers["tmp/"].Typeflag)
	assert.Equal(t, int64(0o1777), headers["tmp/"].Mode)
	assert.Equal(t, int64(0o644), headers["etc/passwd"].Mode)
}

func TestCACertsBundleValid(t *testing.T) {
	t.Serial()
	require.True(t, x509.NewCertPool().AppendCertsFromPEM(caCertsPEM), "embedded CA bundle must contain valid PEM certificates")

	var n int
	rest := caCertsPEM
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			if _, err := x509.ParseCertificate(block.Bytes); err == nil {
				n++
			}
		}
	}
	require.Greater(t, n, 0, "embedded CA bundle must parse to at least one certificate")
}

func TestOCIRepackageDeterministic(t *testing.T) {
	t.Serial()
	store := openTestStore(t)
	ctx := context.Background()

	rp := &OCI{Store: store}
	input := makeInput()

	input.Reader = bytes.NewReader(testBinary)
	out1, err := rp.Repackage(ctx, input)
	require.NoError(t, err)
	m1, err := io.ReadAll(out1.Reader)
	require.NoError(t, err)

	input.Reader = bytes.NewReader(testBinary)
	out2, err := rp.Repackage(ctx, input)
	require.NoError(t, err)
	m2, err := io.ReadAll(out2.Reader)
	require.NoError(t, err)

	// Identical manifest bytes imply identical config + base + binary blob digests.
	assert.Equal(t, m1, m2)
}

func TestOCIRepackageUser(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	store := openTestStore(t)
	ctx := context.Background()

	proj := &db.Project{Name: "testapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "v1.0.0", VersionNum: 1, OciUser: "65532:65532"}
	require.NoError(t, d.CreateRelease(ctx, rel))

	key, size, err := store.Put(ctx, strings.NewReader(string(testBinary)))
	require.NoError(t, err)
	a := &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	rp := &OCI{Store: store, DB: d}
	input := makeInput()
	input.Project = *proj
	input.Release = *rel
	input.Artifact = *a

	_, err = rp.Repackage(ctx, input)
	require.NoError(t, err)

	// The per-release oci_user must surface as config.User in the synthesized config.
	cfgKey, _, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "oci-config")
	require.NoError(t, err)
	rc, _, err := store.Get(ctx, cfgKey)
	require.NoError(t, err)
	cfgBytes, err := io.ReadAll(rc)
	rc.Close()
	require.NoError(t, err)
	var cfg struct {
		Config struct {
			User string `json:"User"`
		} `json:"config"`
	}
	require.NoError(t, json.Unmarshal(cfgBytes, &cfg))
	assert.Equal(t, "65532:65532", cfg.Config.User)
}
