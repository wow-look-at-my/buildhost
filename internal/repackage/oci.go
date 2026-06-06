package repackage

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// caCertsPEM is a real public CA root bundle (Mozilla set, as published by the curl
// project). It is placed at /etc/ssl/certs/ca-certificates.crt -- the path Go's linux
// x509 loader checks by default -- in the shared essentials base layer, so a binary that
// makes outbound TLS calls works inside the synthesized image. See cacerts/README.md.
//
//go:embed cacerts/ca-certificates.crt
var caCertsPEM []byte

type OCI struct {
	Store storage.Storage
	DB    *db.DB
}

func (o *OCI) Format() Format { return FormatOCI }

func (o *OCI) Applicable(a db.Artifact) bool {
	return a.Kind == db.KindBinary
}

// ociDescriptor is the (storage key, size) pair the manifest needs to reference a blob.
type ociDescriptor struct {
	key  string
	size int64
}

func (o *OCI) Repackage(ctx context.Context, input Input) (*Output, error) {
	if input.Artifact.OS == "" || input.Artifact.Arch == "" {
		return nil, fmt.Errorf("artifact missing os/arch")
	}

	// Shared "essentials" base layer (CA certs + minimal rootfs). Memoized, so it is
	// built once per process; the bytes are identical for every project/arch and dedupe
	// to a single stored blob. It must still be Put (idempotent, content-addressed) and
	// linked to THIS artifact on every pull so BlobBelongsToProject passes for it.
	baseData, baseDiffID, err := essentialsLayer()
	if err != nil {
		return nil, fmt.Errorf("essentials layer: %w", err)
	}
	baseKey, baseSize, err := o.Store.Put(ctx, bytes.NewReader(baseData))
	if err != nil {
		return nil, fmt.Errorf("store base layer: %w", err)
	}
	if input.Artifact.ID > 0 && o.DB != nil {
		o.DB.CreatePackagedArtifact(ctx, input.Artifact.ID, "oci-base-layer", baseKey, baseSize, baseKey, "base-layer.tar.zst", "{}")
	}

	binData, binDiffID, err := ociCreateLayer(input.Data, input.Project.Name)
	if err != nil {
		return nil, fmt.Errorf("create layer: %w", err)
	}
	binKey, binSize, err := o.Store.Put(ctx, bytes.NewReader(binData))
	if err != nil {
		return nil, fmt.Errorf("store layer: %w", err)
	}
	if input.Artifact.ID > 0 && o.DB != nil {
		o.DB.CreatePackagedArtifact(ctx, input.Artifact.ID, "oci-layer", binKey, binSize, binKey, "layer.tar.zst", "{}")
	}

	configData := ociCreateConfig(
		string(input.Artifact.OS), string(input.Artifact.Arch),
		[]string{baseDiffID, binDiffID}, input.Project.Name, input.Release.OciUser,
	)

	configKey, configSize, err := o.Store.Put(ctx, bytes.NewReader(configData))
	if err != nil {
		return nil, fmt.Errorf("store config: %w", err)
	}
	if input.Artifact.ID > 0 && o.DB != nil {
		o.DB.CreatePackagedArtifact(ctx, input.Artifact.ID, "oci-config", configKey, configSize, configKey, "config.json", "{}")
	}

	// Base layer first -- must match the diff_ids order in the config.
	manifestData := ociCreateManifest(
		ociDescriptor{configKey, configSize},
		[]ociDescriptor{{baseKey, baseSize}, {binKey, binSize}},
	)

	filename := fmt.Sprintf("%s-%s-%s-%s-oci-manifest.json", input.Project.Name, input.Release.Version, input.Artifact.OS, input.Artifact.Arch)
	return &Output{
		Reader:   bytes.NewReader(manifestData),
		Filename: filename,
		Size:     int64(len(manifestData)),
		Metadata: map[string]string{
			"os":   string(input.Artifact.OS),
			"arch": string(input.Artifact.Arch),
		},
	}, nil
}

func ociCreateLayer(data []byte, name string) (compressed []byte, diffID string, err error) {
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		return nil, "", fmt.Errorf("create zstd writer: %w", err)
	}
	tarHasher := sha256.New()

	tw := tar.NewWriter(io.MultiWriter(tarHasher, zw))
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Size:     int64(len(data)),
		Mode:     0o755,
		Typeflag: tar.TypeReg,
	}); err != nil {
		return nil, "", err
	}
	if _, err := tw.Write(data); err != nil {
		return nil, "", err
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}

	diffID = hex.EncodeToString(tarHasher.Sum(nil))
	return buf.Bytes(), diffID, nil
}

// /etc/passwd and /etc/group: root, nobody and nonroot (matching gcr.io/distroless).
// The nonroot user (65532) is always present so an image can be run with --user 65532
// even when the publisher did not pin a default user via oci_user.
const (
	etcPasswd = "root:x:0:0:root:/root:/sbin/nologin\n" +
		"nobody:x:65534:65534:nobody:/nonexistent:/sbin/nologin\n" +
		"nonroot:x:65532:65532:nonroot:/home/nonroot:/sbin/nologin\n"
	etcGroup = "root:x:0:\n" +
		"nobody:x:65534:\n" +
		"nonroot:x:65532:\n"
	etcNsswitch = "hosts: files dns\n"
)

type essentials struct {
	compressed []byte
	diffID     string
}

// essentialsOnce memoizes the shared base layer: it is constant for the lifetime of the
// process, so it is built exactly once. A build error (e.g. a corrupt embedded bundle) is
// returned to every caller rather than panicking in the request path.
var essentialsOnce = sync.OnceValues(buildEssentials)

func essentialsLayer() ([]byte, string, error) {
	e, err := essentialsOnce()
	return e.compressed, e.diffID, err
}

// buildEssentials builds the deterministic, zstd-compressed "essentials" tar layer:
// CA certificates plus a minimal rootfs (/etc/passwd, /etc/group, /etc/nsswitch.conf and
// a sticky /tmp). It is pure -- it reads only the embedded bundle and fixed literals,
// emits entries in a fixed order with pinned headers -- so the output is byte-identical
// on every call (required: the pull path regenerates and re-hashes it per request).
func buildEssentials() (essentials, error) {
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return essentials{}, fmt.Errorf("create zstd writer: %w", err)
	}
	tarHasher := sha256.New()
	tw := tar.NewWriter(io.MultiWriter(tarHasher, zw))

	// Fixed order: parents before children. A slice (not a map) keeps it deterministic.
	entries := []struct {
		name     string
		mode     int64
		typeflag byte
		data     []byte
	}{
		{"etc/", 0o755, tar.TypeDir, nil},
		{"etc/passwd", 0o644, tar.TypeReg, []byte(etcPasswd)},
		{"etc/group", 0o644, tar.TypeReg, []byte(etcGroup)},
		{"etc/nsswitch.conf", 0o644, tar.TypeReg, []byte(etcNsswitch)},
		{"etc/ssl/", 0o755, tar.TypeDir, nil},
		{"etc/ssl/certs/", 0o755, tar.TypeDir, nil},
		{"etc/ssl/certs/ca-certificates.crt", 0o644, tar.TypeReg, caCertsPEM},
		{"tmp/", 0o1777, tar.TypeDir, nil},
	}
	for _, e := range entries {
		if err := writeTarEntry(tw, e.name, e.mode, e.typeflag, e.data); err != nil {
			return essentials{}, fmt.Errorf("write %s: %w", e.name, err)
		}
	}

	if err := tw.Close(); err != nil {
		return essentials{}, err
	}
	if err := zw.Close(); err != nil {
		return essentials{}, err
	}
	return essentials{compressed: buf.Bytes(), diffID: hex.EncodeToString(tarHasher.Sum(nil))}, nil
}

// writeTarEntry writes one fully-pinned, reproducible USTAR entry. Forcing FormatUSTAR
// guarantees byte-stable output across Go toolchain versions (a field that overflowed
// USTAR would error here rather than silently emitting version-dependent PAX/GNU extended
// headers). The mode is written as a raw integer, so the sticky bit (0o1777 on /tmp)
// round-trips. For directories pass typeflag=tar.TypeDir and data=nil.
func writeTarEntry(tw *tar.Writer, name string, mode int64, typeflag byte, data []byte) error {
	size := int64(len(data))
	if typeflag == tar.TypeDir {
		size = 0
	}
	hdr := &tar.Header{
		Name:     name,
		Mode:     mode,
		Size:     size,
		Typeflag: typeflag,
		ModTime:  time.Unix(0, 0),
		Uid:      0,
		Gid:      0,
		Format:   tar.FormatUSTAR,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if typeflag != tar.TypeDir && len(data) > 0 {
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	return nil
}

func ociCreateConfig(os, arch string, diffIDs []string, name, user string) []byte {
	prefixed := make([]string, len(diffIDs))
	for i, d := range diffIDs {
		prefixed[i] = "sha256:" + d
	}
	cfg := map[string]any{
		"Entrypoint": []string{"/" + name},
		"WorkingDir": "/",
		"Env": []string{
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt",
		},
	}
	if user != "" {
		cfg["User"] = user
	}
	config := map[string]any{
		"architecture": arch,
		"os":           os,
		"rootfs": map[string]any{
			"type":     "layers",
			"diff_ids": prefixed,
		},
		"config": cfg,
	}
	data, _ := json.Marshal(config)
	return data
}

func ociCreateManifest(config ociDescriptor, layers []ociDescriptor) []byte {
	layerDescs := make([]map[string]any, len(layers))
	for i, l := range layers {
		layerDescs[i] = map[string]any{
			"mediaType": "application/vnd.oci.image.layer.v1.tar+zstd",
			"digest":    "sha256:" + l.key,
			"size":      l.size,
		}
	}
	manifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest":    "sha256:" + config.key,
			"size":      config.size,
		},
		"layers": layerDescs,
	}
	data, _ := json.Marshal(manifest)
	return data
}
