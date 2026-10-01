// shellgen fetches the busybox every synthesized image's base layer carries and
// writes it into the tree for go:embed, the way fetch-cacerts.sh does for the CA
// bundle.
//
// It runs at BUILD time on purpose. A pull-time fetch made every image buildhost
// serves depend on Docker Hub, so a registry that cannot reach it served none.
package main

import (
	"archive/tar"
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
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	registryURL = "https://registry-1.docker.io"
	tokenURL    = "https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/busybox:pull"
	imageRepo   = "library/busybox"
	maxManifest = 1 << 20
	maxLayer    = 64 << 20
)

// images pins the busybox:musl manifest per architecture. The pin is what makes
// the fetch reproducible, and what the download is checked against.
var images = map[string]string{
	"amd64": "sha256:a34ce92094b7b100a98fbd21411a92825f6827b1bc5f6918c253516c90556998",
	"arm64": "sha256:3cb83a1fb0a5d7064741699ec39f3276d393df432c7c287e8486154465910a89",
}

func main() {
	dir := "shell"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := run(dir); err != nil {
		fmt.Fprintln(os.Stderr, "shellgen:", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ctx := context.Background()
	token, err := fetchToken(ctx)
	if err != nil {
		return err
	}
	arches := make([]string, 0, len(images))
	for arch := range images {
		arches = append(arches, arch)
	}
	sort.Strings(arches)

	for _, arch := range arches {
		busybox, applets, err := fetch(ctx, token, images[arch])
		if err != nil {
			return fmt.Errorf("fetch the shell for %s: %w", arch, err)
		}
		binPath := filepath.Join(dir, "busybox-"+arch)
		if err := os.WriteFile(binPath, busybox, 0o755); err != nil {
			return err
		}
		listPath := filepath.Join(dir, "applets-"+arch)
		if err := os.WriteFile(listPath, []byte(strings.Join(applets, "\n")+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Printf("fetched busybox for %s -> %s (%d bytes, %d applets)\n", arch, binPath, len(busybox), len(applets))
	}
	return nil
}

func client() *http.Client { return &http.Client{Timeout: 2 * time.Minute} }

func fetchToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client().Do(req)
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
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxManifest)).Decode(&t); err != nil {
		return "", fmt.Errorf("registry token: %w", err)
	}
	if t.Token == "" {
		return "", errors.New("registry token: empty token")
	}
	return t.Token, nil
}

func get(ctx context.Context, token, p, accept string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, registryURL+p, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", p, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: HTTP %d", p, resp.StatusCode)
	}
	return resp.Body, nil
}

func fetch(ctx context.Context, token, manifestDigest string) ([]byte, []string, error) {
	manifest, err := get(ctx, token, "/v2/"+imageRepo+"/manifests/"+manifestDigest,
		"application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	if err != nil {
		return nil, nil, err
	}
	defer manifest.Close()
	manifestBytes, err := io.ReadAll(io.LimitReader(manifest, maxManifest))
	if err != nil {
		return nil, nil, fmt.Errorf("read manifest: %w", err)
	}
	if got := digestOf(manifestBytes); got != manifestDigest {
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
	blob, err := get(ctx, token, "/v2/"+imageRepo+"/blobs/"+layer.Digest, "")
	if err != nil {
		return nil, nil, err
	}
	defer blob.Close()
	hasher := sha256.New()
	body := io.TeeReader(io.LimitReader(blob, maxLayer), hasher)
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
	if _, err := io.Copy(io.Discard, body); err != nil {
		return nil, nil, fmt.Errorf("read layer: %w", err)
	}
	if got := "sha256:" + hex.EncodeToString(hasher.Sum(nil)); got != layer.Digest {
		return nil, nil, fmt.Errorf("layer digest is %s, manifest says %s", got, layer.Digest)
	}
	return busybox, applets, nil
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// readBusyboxLayer finds the busybox binary in an image layer and the names that
// run it.
//
// The layer archives the binary under an applet name and hardlinks the rest to
// it: the pinned image stores "bin/[", not "bin/busybox". bin/ also holds
// binaries that are not busybox -- the musl image ships its own bin/getconf. So
// busybox is the file bin/busybox resolves to, and the applets are the names
// that resolve to that same file.
func readBusyboxLayer(r io.Reader) ([]byte, []string, error) {
	tr := tar.NewReader(r)
	var (
		files = map[string][]byte{}
		links = map[string]string{}
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
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, nil, fmt.Errorf("read %s: %w", name, err)
			}
			files[name] = data
		case tar.TypeLink, tar.TypeSymlink:
			target := hdr.Linkname
			if !path.IsAbs(target) && hdr.Typeflag == tar.TypeSymlink {
				target = path.Join("bin", target)
			}
			links[name] = path.Clean(strings.TrimPrefix(target, "/"))
		}
	}
	root, ok := resolveLayerEntry("bin/busybox", files, links)
	if !ok {
		return nil, nil, errors.New("shell image has no bin/busybox")
	}
	applets := []string{path.Base(root)}
	for name := range links {
		if target, ok := resolveLayerEntry(name, files, links); ok && target == root {
			applets = append(applets, path.Base(name))
		}
	}
	sort.Strings(applets)
	// An APE trampoline is a shell script, so a layer with no sh applet ships an
	// image whose every container dies at exec with the file present.
	if !slices.Contains(applets, "sh") {
		return nil, nil, errors.New("shell image has no bin/sh applet")
	}
	return files[root], applets, nil
}

// resolveLayerEntry follows name through the layer's links to the regular file
// it names. The bound stops a link cycle from hanging the fetch.
func resolveLayerEntry(name string, files map[string][]byte, links map[string]string) (string, bool) {
	for range 16 {
		if _, ok := files[name]; ok {
			return name, true
		}
		next, ok := links[name]
		if !ok {
			return "", false
		}
		name = next
	}
	return "", false
}
