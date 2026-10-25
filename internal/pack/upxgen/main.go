// upxgen fetches the UPX packer the server carries and writes it into the tree
// for go:embed, the way shellgen does for the image's busybox.
//
// It runs at BUILD time because the production image is distroless and ships
// no binutils. A packer looked up on PATH would silently stop packing there.
// UPX publishes Linux builds on GitHub Releases; on macOS the only prebuilt
// binary is the Homebrew bottle, so that platform is fetched from ghcr.
package main

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
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// release is the UPX version embedded in the server.
const release = "5.2.1"

type source struct {
	// platform is the goos-goarch the binary runs on.
	platform string
	// url names the download.
	url string
	// digest is the SHA-256 of the downloaded bytes.
	digest string
	// bottle marks a Homebrew bottle, which needs a ghcr token to fetch.
	bottle bool
	// member is the path of the packer inside the archive.
	member string
	// xz marks a .tar.xz archive; otherwise it is a gzipped tar.
	xz bool
}

var sources = []source{
	{
		platform: "linux-amd64",
		url:      "https://github.com/upx/upx/releases/download/v" + release + "/upx-" + release + "-amd64_linux.tar.xz",
		digest:   "402162aad30af47e60dbd767fb2e64ca394ace9727ba1f40283641f1d1b91657",
		member:   "upx-" + release + "-amd64_linux/upx",
		xz:       true,
	},
	{
		platform: "linux-arm64",
		url:      "https://github.com/upx/upx/releases/download/v" + release + "/upx-" + release + "-arm64_linux.tar.xz",
		digest:   "a72d112c5970a904a31da0b9c84f919bc16b9a311787c12245508544a78c7d36",
		member:   "upx-" + release + "-arm64_linux/upx",
		xz:       true,
	},
	{
		platform: "darwin-arm64",
		url:      "https://ghcr.io/v2/homebrew/core/upx/blobs/sha256:dde87ba555dc9b24e4c337093235109f7781df57dcd42af79286eb0ff30a795a",
		digest:   "dde87ba555dc9b24e4c337093235109f7781df57dcd42af79286eb0ff30a795a",
		bottle:   true,
		member:   "upx/" + release + "/bin/upx",
	},
}

const (
	ghcrTokenURL = "https://ghcr.io/token?service=ghcr.io&scope=repository:homebrew/core/upx:pull"
	maxDownload  = 64 << 20
)

func main() {
	dir := "upx"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := run(context.Background(), dir); err != nil {
		fmt.Fprintln(os.Stderr, "upxgen:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		return err
	}
	defer enc.Close()

	var token string
	for _, src := range sources {
		if src.bottle && token == "" {
			token, err = ghcrToken(ctx)
			if err != nil {
				return err
			}
		}
		archive, err := download(ctx, src.url, token)
		if err != nil {
			return fmt.Errorf("fetch the packer for %s: %w", src.platform, err)
		}
		if got := digestOf(archive); got != src.digest {
			return fmt.Errorf("%s download is %s, pinned %s", src.platform, got, src.digest)
		}
		bin, err := extract(archive, src)
		if err != nil {
			return fmt.Errorf("extract the packer for %s: %w", src.platform, err)
		}
		out := filepath.Join(dir, "upx-"+src.platform+".zst")
		if err := os.WriteFile(out, enc.EncodeAll(bin, nil), 0o644); err != nil {
			return err
		}
		fmt.Printf("fetched upx %s for %s -> %s (%d bytes packed)\n", release, src.platform, out, len(bin))
	}
	return nil
}

func client() *http.Client { return &http.Client{Timeout: 2 * time.Minute} }

func ghcrToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ghcrTokenURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client().Do(req)
	if err != nil {
		return "", fmt.Errorf("ghcr token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ghcr token: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("ghcr token: %w", err)
	}
	if body.Token == "" {
		return "", errors.New("ghcr token: empty")
	}
	return body.Token, nil
}

func download(ctx context.Context, rawURL, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", rawURL, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownload))
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// extract returns the packer binary from a release tarball or bottle.
func extract(archive []byte, src source) ([]byte, error) {
	var stream io.Reader = bytes.NewReader(archive)
	if src.xz {
		r, err := xz.NewReader(stream)
		if err != nil {
			return nil, err
		}
		stream = r
	} else {
		gz, err := gzip.NewReader(stream)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		stream = gz
	}
	tr := tar.NewReader(stream)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("no %s in the archive", src.member)
		}
		if err != nil {
			return nil, err
		}
		if path.Clean(hdr.Name) != src.member {
			continue
		}
		return io.ReadAll(io.LimitReader(tr, maxDownload))
	}
}
