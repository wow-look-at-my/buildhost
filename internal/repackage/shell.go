package repackage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// An Actually Portable Executable starts through the shell script in its own

const (
	shellRegistryURL = "https://registry-1.docker.io"
	shellTokenURL    = "https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/busybox:pull"
	shellImageRepo   = "library/busybox"
	// shellMaxManifest bounds a manifest read; shellMaxLayer bounds the layer download.
	shellMaxManifest = 1 << 20
	shellMaxLayer    = 64 << 20
)

// shellImages pins the busybox:musl manifest per architecture. The pin is what
// makes the layer reproducible, and what the download is checked against.
var shellImages = map[db.Arch]string{
	db.ArchAMD64: "sha256:a34ce92094b7b100a98fbd21411a92825f6827b1bc5f6918c253516c90556998",
	db.ArchARM64: "sha256:3cb83a1fb0a5d7064741699ec39f3276d393df432c7c287e8486154465910a89",
}

// ShellCache serves the shell layer for an APE image. The busybox binary and
type ShellCache struct {
	// Dir is the durable cache root, {DataDir}/shell in production.
	Dir string
	// Registry, TokenURL and Images default to Docker Hub and the pins above;
	Registry string
	TokenURL string
	Images   map[db.Arch]string
	Client   *http.Client

	mu     sync.Mutex
	layers map[db.Arch]*shellLayer
}

type shellLayer struct {
	compressed []byte
	diffID     string
}

// NewShellCache returns a cache rooted at dir that fetches from Docker Hub.
func NewShellCache(dir string) *ShellCache {
	return &ShellCache{Dir: dir}
}

// Layer returns the zstd-compressed shell layer for arch and its diffID. The
func (c *ShellCache) Layer(ctx context.Context, arch db.Arch) ([]byte, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if l, ok := c.layers[arch]; ok {
		return l.compressed, l.diffID, nil
	}
	busybox, applets, err := c.load(ctx, arch)
	if err != nil {
		return nil, "", err
	}
	l, err := buildShellLayer(busybox, applets)
	if err != nil {
		return nil, "", err
	}
	if c.layers == nil {
		c.layers = map[db.Arch]*shellLayer{}
	}
	c.layers[arch] = l
	return l.compressed, l.diffID, nil
}

func (c *ShellCache) images() map[db.Arch]string {
	if c.Images != nil {
		return c.Images
	}
	return shellImages
}

func (c *ShellCache) registry() string {
	if c.Registry != "" {
		return c.Registry
	}
	return shellRegistryURL
}

func (c *ShellCache) tokenURL() string {
	if c.TokenURL != "" {
		return c.TokenURL
	}
	if c.Registry != "" {
		return ""
	}
	return shellTokenURL
}

func (c *ShellCache) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

// cacheDir is keyed by the pinned digest, so a new pin never reads an old cache.
func (c *ShellCache) cacheDir(arch db.Arch, digest string) string {
	return filepath.Join(c.Dir, string(arch), strings.TrimPrefix(digest, "sha256:"))
}

// load returns the busybox bytes and applet names for arch from the disk
// cache, fetching and filling the cache when they are not there yet.
func (c *ShellCache) load(ctx context.Context, arch db.Arch) ([]byte, []string, error) {
	digest, ok := c.images()[arch]
	if !ok {
		return nil, nil, fmt.Errorf("no shell image is pinned for %s", arch)
	}
	if c.Dir == "" {
		return nil, nil, errors.New("shell cache has no directory")
	}
	dir := c.cacheDir(arch, digest)
	busybox, err := os.ReadFile(filepath.Join(dir, "busybox"))
	if err == nil {
		var listed []byte
		listed, err = os.ReadFile(filepath.Join(dir, "applets"))
		if err == nil {
			return busybox, strings.Fields(string(listed)), nil
		}
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("read shell cache: %w", err)
	}

	busybox, applets, err := c.fetch(ctx, digest)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch shell image for %s: %w", arch, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	if err := writeFileAtomic(filepath.Join(dir, "busybox"), busybox, 0o755); err != nil {
		return nil, nil, err
	}
	if err := writeFileAtomic(filepath.Join(dir, "applets"), []byte(strings.Join(applets, "\n")+"\n"), 0o644); err != nil {
		return nil, nil, err
	}
	return busybox, applets, nil
}

// writeFileAtomic publishes the file with a rename, so a concurrent reader
// never sees a partial write.
func writeFileAtomic(name string, data []byte, mode os.FileMode) error {
	tmp := fmt.Sprintf("%s.%d.tmp", name, os.Getpid())
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, name); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (c *ShellCache) fetch(ctx context.Context, manifestDigest string) ([]byte, []string, error) {
	token, err := c.fetchToken(ctx)
	if err != nil {
		return nil, nil, err
	}
	manifest, err := c.get(ctx, token, "/v2/"+shellImageRepo+"/manifests/"+manifestDigest,
		"application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	if err != nil {
		return nil, nil, err
	}
	defer manifest.Close()
	manifestBytes, err := io.ReadAll(io.LimitReader(manifest, shellMaxManifest))
	if err != nil {
		return nil, nil, fmt.Errorf("read manifest: %w", err)
	}
	if got := sha256Digest(manifestBytes); got != manifestDigest {
		return nil, nil, fmt.Errorf("manifest digest is %s, pinned %s", got, manifestDigest)
	}
	var m struct {
		Layers []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return nil, nil, fmt.Errorf("parse manifest: %w", err)
	}
	if len(m.Layers) != 1 {
		return nil, nil, fmt.Errorf("shell image has %d layers, expected one", len(m.Layers))
	}
	layer := m.Layers[0]
	blob, err := c.get(ctx, token, "/v2/"+shellImageRepo+"/blobs/"+layer.Digest, "")
	if err != nil {
		return nil, nil, err
	}
	defer blob.Close()
	hasher := sha256.New()
	body := io.TeeReader(io.LimitReader(blob, shellMaxLayer), hasher)
	var tarStream io.Reader = body
	if strings.HasSuffix(layer.MediaType, "tar+gzip") {
		gz, err := gzip.NewReader(body)
		if err != nil {
			return nil, nil, fmt.Errorf("open layer: %w", err)
		}
		defer gz.Close()
		tarStream = gz
	} else if !strings.HasSuffix(layer.MediaType, ".tar") && layer.MediaType != "application/vnd.oci.image.layer.v1.tar" {
		return nil, nil, fmt.Errorf("shell image layer is %s, expected a tar or tar+gzip layer", layer.MediaType)
	}
	busybox, applets, err := readBusyboxLayer(tarStream)
	if err != nil {
		return nil, nil, err
	}
	// Drain what the tar reader left, so the hash covers the whole blob.
	if _, err := io.Copy(io.Discard, body); err != nil {
		return nil, nil, fmt.Errorf("read layer: %w", err)
	}
	if got := "sha256:" + hex.EncodeToString(hasher.Sum(nil)); got != layer.Digest {
		return nil, nil, fmt.Errorf("layer digest is %s, manifest says %s", got, layer.Digest)
	}
	return busybox, applets, nil
}

func (c *ShellCache) fetchToken(ctx context.Context) (string, error) {
	url := c.tokenURL()
	if url == "" {
		return "", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("registry token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry token: HTTP %d", resp.StatusCode)
	}
	var t struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, shellMaxManifest)).Decode(&t); err != nil {
		return "", fmt.Errorf("registry token: %w", err)
	}
	if t.Token == "" {
		return "", errors.New("registry token: empty token")
	}
	return t.Token, nil
}

func (c *ShellCache) get(ctx context.Context, token, p, accept string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.registry()+p, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", p, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: HTTP %d", p, resp.StatusCode)
	}
	return resp.Body, nil
}

func sha256Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// readBusyboxLayer finds the busybox binary in an image layer and the names
func readBusyboxLayer(r io.Reader) ([]byte, []string, error) {
	tr := tar.NewReader(r)
	var (
		binary     []byte
		binaryName string
		links      = map[string]string{}
	)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read layer tar: %w", err)
		}
		name := path.Clean(hdr.Name)
		if path.Dir(name) != "bin" {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeReg:
			if binary != nil {
				return nil, nil, fmt.Errorf("shell image has two files under bin/: %s and %s", binaryName, name)
			}
			binary, err = io.ReadAll(tr)
			if err != nil {
				return nil, nil, fmt.Errorf("read %s: %w", name, err)
			}
			binaryName = name
		case tar.TypeLink, tar.TypeSymlink:
			target := hdr.Linkname
			if !path.IsAbs(target) && hdr.Typeflag == tar.TypeSymlink {
				target = path.Join("bin", target)
			}
			links[name] = path.Clean(target)
		}
	}
	if binary == nil {
		return nil, nil, errors.New("shell image has no binary under bin/")
	}
	applets := []string{path.Base(binaryName)}
	for name, target := range links {
		if target == binaryName {
			applets = append(applets, path.Base(name))
		}
	}
	sort.Strings(applets)
	return binary, applets, nil
}

// buildShellLayer writes the deterministic shell layer: /bin/busybox and a
// relative symlink per applet, with the same pinned headers the essentials
// layer uses, so the diffID is the same on every server.
func buildShellLayer(busybox []byte, applets []string) (*shellLayer, error) {
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, fmt.Errorf("create zstd writer: %w", err)
	}
	tarHasher := sha256.New()
	tw := tar.NewWriter(io.MultiWriter(tarHasher, zw))

	if err := writeTarEntry(tw, "bin/", 0o755, tar.TypeDir, nil); err != nil {
		return nil, err
	}
	if err := writeTarEntry(tw, "bin/busybox", 0o755, tar.TypeReg, busybox); err != nil {
		return nil, err
	}
	names := append([]string(nil), applets...)
	sort.Strings(names)
	for _, name := range names {
		if name == "busybox" || name == "" || strings.ContainsAny(name, "/\x00") {
			continue
		}
		if err := writeTarSymlink(tw, "bin/"+name, "busybox"); err != nil {
			return nil, fmt.Errorf("write bin/%s: %w", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return &shellLayer{compressed: buf.Bytes(), diffID: hex.EncodeToString(tarHasher.Sum(nil))}, nil
}

func writeTarSymlink(tw *tar.Writer, name, target string) error {
	return tw.WriteHeader(&tar.Header{
		Name:     name,
		Linkname: target,
		Mode:     0o777,
		Typeflag: tar.TypeSymlink,
		ModTime:  time.Unix(0, 0),
		Uid:      0,
		Gid:      0,
		Format:   tar.FormatUSTAR,
	})
}
