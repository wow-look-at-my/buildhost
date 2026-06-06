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

// TestOCIRepackageEssentials verifies the synthesized image carries the shared
// essentials base layer (CA certs + minimal rootfs) in addition to the binary layer:
// the base layer blob is registered, the manifest lists both layers base-first, and the
// config has matching ordered diff_ids, the SSL_CERT_FILE env, the preserved entrypoint
// and no default user.
func TestOCIRepackageEssentials(t *testing.T) {
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

	// All three blobs are registered against this artifact so the pull gate serves them.
	cfgKey, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "oci-config")
	require.NoError(t, err)
	baseKey, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "oci-base-layer")
	require.NoError(t, err)
	_, _, _, _, err = d.GetPackagedArtifact(ctx, a.ID, "oci-layer")
	require.NoError(t, err)

	// Manifest references two layers; the base (essentials) layer is first.
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

	// Config: two diff_ids (base first), Env carries SSL_CERT_FILE, Entrypoint preserved,
	// WorkingDir set, and no default User (root) when oci_user is unset.
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

func TestEssentialsLayerContents(t *testing.T) {
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
	store := openTestStore(t)
	ctx := context.Background()

	rp := &OCI{Store: store}
	input := makeInput()

	out1, err := rp.Repackage(ctx, input)
	require.NoError(t, err)
	m1, err := io.ReadAll(out1.Reader)
	require.NoError(t, err)

	out2, err := rp.Repackage(ctx, input)
	require.NoError(t, err)
	m2, err := io.ReadAll(out2.Reader)
	require.NoError(t, err)

	// Identical manifest bytes imply identical config + base + binary blob digests.
	assert.Equal(t, m1, m2)
}

func TestOCIRepackageUser(t *testing.T) {
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
	cfgKey, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "oci-config")
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
