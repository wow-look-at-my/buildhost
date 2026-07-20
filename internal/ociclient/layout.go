package ociclient

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// maxManifestSize mirrors the server's manifest cap (manifests are tiny JSON
// documents; anything bigger is rejected before it is sent).
const maxManifestSize = 4 << 20 // 4 MiB

var (
	validDigest  = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	validBlobHex = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// descriptor is an OCI content descriptor as found in an index or manifest.
type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// imageManifest is the subset of an OCI image manifest / image index the push
// walk needs: an index carries Manifests, an image manifest carries
// Config+Layers.
type imageManifest struct {
	MediaType string       `json:"mediaType"`
	Config    *descriptor  `json:"config,omitempty"`
	Layers    []descriptor `json:"layers,omitempty"`
	Manifests []descriptor `json:"manifests,omitempty"`
}

func isIndexMediaType(mt string) bool {
	return mt == "application/vnd.oci.image.index.v1+json" ||
		mt == "application/vnd.docker.distribution.manifest.list.v2+json"
}

// layout is an OCI image layout on disk (the `docker buildx build
// --output type=oci` / `docker save` format): an index.json entry point plus
// content-addressed blobs under blobs/sha256/.
type layout struct {
	dir     string
	cleanup func() // removes the temp extraction dir; nil when opened in place
}

// openLayout opens an OCI image layout from a directory or a tarball. A
// tarball is extracted to a temp directory (only the layout members --
// index.json, oci-layout, blobs/sha256/<hex> -- are extracted, so hostile
// entry names cannot escape).
func openLayout(path string) (*layout, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open image %s: %w", path, err)
	}
	if st.IsDir() {
		l := &layout{dir: path}
		if _, err := os.Stat(filepath.Join(path, "index.json")); err != nil {
			return nil, fmt.Errorf("%s is not an OCI image layout (no index.json)", path)
		}
		return l, nil
	}
	return extractLayoutTar(path)
}

// Close removes any temp extraction directory.
func (l *layout) Close() {
	if l.cleanup != nil {
		l.cleanup()
	}
}

// extractLayoutTar extracts the OCI layout members of a tarball into a temp
// directory. Entry names are matched against an exact whitelist -- index.json,
// oci-layout, and blobs/sha256/<64-hex> -- so no path from the archive is ever
// trusted; everything else (docker save's legacy manifest.json, repositories,
// directory entries) is skipped.
func extractLayoutTar(path string) (*layout, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open image %s: %w", path, err)
	}
	defer f.Close()

	dir, err := os.MkdirTemp("", "buildhost-oci-*")
	if err != nil {
		return nil, fmt.Errorf("create extraction dir: %w", err)
	}
	l := &layout{dir: dir, cleanup: func() { os.RemoveAll(dir) }}
	if err := os.MkdirAll(filepath.Join(dir, "blobs", "sha256"), 0o755); err != nil {
		l.Close()
		return nil, err
	}

	tr := tar.NewReader(f)
	sawIndex := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			l.Close()
			return nil, fmt.Errorf("read image tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		var dest string
		switch {
		case name == "index.json":
			dest = filepath.Join(dir, "index.json")
			sawIndex = true
		case name == "oci-layout":
			dest = filepath.Join(dir, "oci-layout")
		case strings.HasPrefix(name, "blobs/sha256/") && validBlobHex.MatchString(name[len("blobs/sha256/"):]):
			dest = filepath.Join(dir, "blobs", "sha256", name[len("blobs/sha256/"):])
		default:
			continue
		}
		if err := writeFileFrom(dest, tr); err != nil {
			l.Close()
			return nil, fmt.Errorf("extract %s: %w", name, err)
		}
	}
	if !sawIndex {
		l.Close()
		return nil, fmt.Errorf("%s is not an OCI image tarball (no index.json)", path)
	}
	return l, nil
}

func writeFileFrom(dest string, r io.Reader) error {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// root returns the layout's single top-level descriptor. Both `docker buildx
// --output type=oci` and `docker save <one image>` write exactly one entry in
// index.json (an image manifest, or a nested index for multi-platform /
// attestation-carrying builds); a layout holding several images has no single
// pushable root, so it is rejected.
func (l *layout) root() (descriptor, error) {
	data, err := os.ReadFile(filepath.Join(l.dir, "index.json"))
	if err != nil {
		return descriptor{}, fmt.Errorf("read index.json: %w", err)
	}
	var idx imageManifest
	if err := json.Unmarshal(data, &idx); err != nil {
		return descriptor{}, fmt.Errorf("parse index.json: %w", err)
	}
	if len(idx.Manifests) != 1 {
		return descriptor{}, fmt.Errorf("image layout has %d top-level manifests, want exactly 1 (one image per push)", len(idx.Manifests))
	}
	return idx.Manifests[0], nil
}

// blobPath resolves a digest to its blob file, verifying the file exists and
// matches the descriptor's size when one is given (a corrupt/truncated layout
// fails here instead of mid-upload).
func (l *layout) blobPath(digest string, wantSize int64) (string, error) {
	if !validDigest.MatchString(digest) {
		return "", fmt.Errorf("unsupported digest %q (only sha256 is supported)", digest)
	}
	p := filepath.Join(l.dir, "blobs", "sha256", digest[len("sha256:"):])
	st, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("blob %s missing from image layout: %w", digest, err)
	}
	if wantSize >= 0 && st.Size() != wantSize {
		return "", fmt.Errorf("blob %s is %d bytes on disk but its descriptor says %d", digest, st.Size(), wantSize)
	}
	return p, nil
}

// readManifest reads a manifest blob (bounded by maxManifestSize) and parses it.
func (l *layout) readManifest(d descriptor) ([]byte, *imageManifest, error) {
	p, err := l.blobPath(d.Digest, d.Size)
	if err != nil {
		return nil, nil, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, nil, err
	}
	if st.Size() > maxManifestSize {
		return nil, nil, fmt.Errorf("manifest %s is %d bytes, over the %d-byte manifest cap", d.Digest, st.Size(), int64(maxManifestSize))
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, nil, err
	}
	var m imageManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, nil, fmt.Errorf("manifest %s is not valid JSON: %w", d.Digest, err)
	}
	return data, &m, nil
}
