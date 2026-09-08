package repackage

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/go-containers/set"
)

// caCertsPEM is a real public CA root bundle, the Mozilla set the curl project publishes.
//
//go:generate ../../scripts/fetch-cacerts.sh cacerts/ca-certificates.crt
//go:embed cacerts/ca-certificates.crt
var caCertsPEM []byte

type OCI struct {
	Store storage.Storage
	DB    *db.DB
}

func (o *OCI) Format() Format { return FormatOCI }

// Applicable gates the format to linux, the OS the image's rootfs and shell are.
func (o *OCI) Applicable(a db.Artifact) bool {
	return a.Kind == db.KindBinary && a.OS == db.OSLinux
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

	// Every image starts here: rootfs, CA bundle and shell, per architecture.
	baseData, baseDiffID, err := imageBase(input.Artifact.Arch)
	if err != nil {
		return nil, fmt.Errorf("base layer: %w", err)
	}
	baseKey, baseSize, err := o.Store.Put(ctx, bytes.NewReader(baseData))
	if err != nil {
		return nil, fmt.Errorf("store base layer: %w", err)
	}
	if input.Artifact.ID > 0 && o.DB != nil {
		// Suffixed like the config: the layer is per architecture.
		o.DB.CreatePackagedArtifact(ctx, input.Artifact.ID, "oci-base-layer"+input.CacheSuffix, baseKey, baseSize, baseKey, "base-layer.tar.zst", "{}")
	}

	entrypoint := []string{"/" + input.Project.Name}
	layers := []ociDescriptor{{baseKey, baseSize}}
	diffIDs := []string{baseDiffID}

	isAPE, artifactReader, err := peekAPE(input.Reader)
	if err != nil {
		return nil, fmt.Errorf("inspect artifact: %w", err)
	}
	if isAPE {
		// Ship the ELF the trampoline would have staged, so nothing is staged.
		artifactReader, err = apeAsELF(artifactReader, input.Artifact.Arch)
		if err != nil {
			return nil, fmt.Errorf("stage the APE as an ELF for %s: %w", input.Artifact.Arch, err)
		}
	}

	binKey, binSize, binDiffID, err := ociWriteLayer(ctx, o.Store, artifactReader, input.Size, input.Project.Name)
	if err != nil {
		return nil, fmt.Errorf("create layer: %w", err)
	}
	if input.Artifact.ID > 0 && o.DB != nil {
		// Suffixed: an unsuffixed key lets a later platform's upsert unlink this blob.
		o.DB.CreatePackagedArtifact(ctx, input.Artifact.ID, "oci-layer"+input.CacheSuffix, binKey, binSize, binKey, "layer.tar.zst", "{}")
	}
	layers = append(layers, ociDescriptor{binKey, binSize})
	diffIDs = append(diffIDs, binDiffID)

	configData := ociCreateConfig(
		string(input.Artifact.OS), string(input.Artifact.Arch),
		diffIDs, entrypoint, input.Release.OciUser,
	)

	configKey, configSize, err := o.Store.Put(ctx, bytes.NewReader(configData))
	if err != nil {
		return nil, fmt.Errorf("store config: %w", err)
	}
	if input.Artifact.ID > 0 && o.DB != nil {
		o.DB.CreatePackagedArtifact(ctx, input.Artifact.ID, "oci-config"+input.CacheSuffix, configKey, configSize, configKey, "config.json", "{}")
	}

	// Same order as the diff_ids in the config: base, then binary.
	manifestData := ociCreateManifest(ociDescriptor{configKey, configSize}, layers)

	// Persist the manifest document itself (alongside its config + layers above)
	// and link it to the project, so the pull path can serve it by its content
	// digest. A multi-arch index lists each platform's image manifest by digest,
	// and the client resolves it via GET /v2/{name}/manifests/<digest>, which is
	// gated on BlobBelongsToProject and served straight from storage. Without
	if input.Project.ID > 0 && o.DB != nil {
		manifestKey, manifestSize, err := o.Store.Put(ctx, bytes.NewReader(manifestData))
		if err != nil {
			return nil, fmt.Errorf("store manifest: %w", err)
		}
		if err := o.DB.LinkOCIBlob(ctx, input.Project.ID, manifestKey, "application/vnd.oci.image.manifest.v1+json", manifestSize, true); err != nil {
			return nil, fmt.Errorf("link manifest: %w", err)
		}
	}

	filename := fmt.Sprintf("%s-%s-%s-%s-oci-manifest.json", input.Project.Name, input.Release.Version, input.Artifact.OS, input.Artifact.Arch)
	return &Output{
		Reader:   io.NopCloser(bytes.NewReader(manifestData)),
		Filename: filename,
		Size:     int64(len(manifestData)),
		Metadata: map[string]string{
			"os":   string(input.Artifact.OS),
			"arch": string(input.Artifact.Arch),
		},
	}, nil
}

// imageBase returns the layer every synthesized image starts from: the
// essentials rootfs with the shell appended, for arch.
func imageBase(arch db.Arch) ([]byte, string, error) {
	essentials, _, err := essentialsLayer()
	if err != nil {
		return nil, "", fmt.Errorf("essentials: %w", err)
	}
	shell, _, err := ShellLayer(arch)
	if err != nil {
		return nil, "", fmt.Errorf("shell for %s: %w", arch, err)
	}
	return joinLayers(essentials, shell)
}

// imageBinPath is where an image keeps the binary itself, out of the way of the
// launcher that starts it.
func imageBinPath(name string) string {
	return "usr/local/lib/" + name + "/" + path.Base(name)
}

// imageLauncher is the shebang script every entrypoint spelling reaches. A
// rolling updater clones the entrypoint off the container it replaces, so a
// spelling any image shipped must keep starting the server.
func imageLauncher(name string) []byte {
	return fmt.Appendf(nil, "#!/bin/sh\n# Generated by buildhost.\nexec %s \"$@\"\n",
		shellQuote("/"+imageBinPath(name)))
}

// joinLayers merges the entries of zstd-compressed tar layers, returning the
// result with the diffID of its uncompressed bytes. An earlier layer wins a
// name both carry.
func joinLayers(layers ...[]byte) ([]byte, string, error) {
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, "", fmt.Errorf("create zstd writer: %w", err)
	}
	hasher := sha256.New()
	tw := tar.NewWriter(io.MultiWriter(hasher, zw))

	seen := set.New[string]()
	for _, layer := range layers {
		zr, err := zstd.NewReader(bytes.NewReader(layer))
		if err != nil {
			return nil, "", fmt.Errorf("open layer: %w", err)
		}
		tr := tar.NewReader(zr)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				zr.Close()
				return nil, "", fmt.Errorf("read layer: %w", err)
			}
			if seen.Contains(hdr.Name) {
				continue
			}
			seen.Add(hdr.Name)
			if err := tw.WriteHeader(hdr); err != nil {
				zr.Close()
				return nil, "", err
			}
			if hdr.Typeflag == tar.TypeReg {
				if _, err := io.Copy(tw, tr); err != nil {
					zr.Close()
					return nil, "", err
				}
			}
		}
		zr.Close()
	}

	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), hex.EncodeToString(hasher.Sum(nil)), nil
}

// ociWriteLayer streams r -> tar -> zstd straight into store.Put while teeing
// the uncompressed bytes through a hasher for the config's diff_id. The binary
// lands under /usr/local/lib, with a launcher at /<name> and at
// /usr/local/bin/<name> so a bare name on PATH also starts it. Every image gets
// that layout, whatever kind of binary it was built from.
func ociWriteLayer(ctx context.Context, store storage.Storage, r io.Reader, size int64, name string) (key string, compressedSize int64, diffID string, err error) {
	binPath := imageBinPath(name)
	diffHasher := sha256.New()
	pr, pw := io.Pipe()
	go func() {
		zw, zerr := zstd.NewWriter(pw)
		if zerr != nil {
			pw.CloseWithError(fmt.Errorf("create zstd writer: %w", zerr))
			return
		}
		tw := tar.NewWriter(io.MultiWriter(diffHasher, zw))
		if err := tw.WriteHeader(&tar.Header{
			Name:     binPath,
			Size:     size,
			Mode:     0o755,
			Typeflag: tar.TypeReg,
		}); err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(tw, r); err != nil {
			pw.CloseWithError(err)
			return
		}
		launcher := imageLauncher(name)
		for _, at := range []string{name, "usr/local/bin/" + path.Base(name)} {
			if err := writeTarEntry(tw, at, 0o755, tar.TypeReg, launcher); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		if err := tw.Close(); err != nil {
			pw.CloseWithError(err)
			return
		}
		pw.CloseWithError(zw.Close())
	}()

	key, compressedSize, err = store.Put(ctx, pr)
	if err != nil {
		pr.CloseWithError(err)
		return "", 0, "", err
	}
	return key, compressedSize, hex.EncodeToString(diffHasher.Sum(nil)), nil
}

// /etc/passwd and /etc/group: root, nobody and nonroot (matching gcr.io/distroless).
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
// The shell is appended to it per architecture; see OCI.base.
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

func ociCreateConfig(os, arch string, diffIDs []string, entrypoint []string, user string) []byte {
	prefixed := make([]string, len(diffIDs))
	for i, d := range diffIDs {
		prefixed[i] = "sha256:" + d
	}
	cfg := map[string]any{
		"Entrypoint": entrypoint,
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
