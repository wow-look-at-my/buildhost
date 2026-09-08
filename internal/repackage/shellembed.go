package repackage

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// shellFS carries the busybox every synthesized image's base layer ships, baked
// in at build time by shellgen the way fetch-cacerts.sh bakes in the CA bundle.
//
// Fetching it at pull time put a third party in the path of every image this
// server serves: a deployment that cannot reach Docker Hub answered every pull
// with a synthesis failure, and a test that synthesized an image failed whenever
// the network did.
//
//go:generate go run ./shellgen shell
//go:embed shell
var shellFS embed.FS

// shellArches are the architectures shellgen bakes a shell in for.
var shellArches = []db.Arch{db.ArchAMD64, db.ArchARM64}

type shellLayer struct {
	compressed []byte
	diffID     string
}

// shellLayers memoizes the built layer per architecture. Each is deterministic,
// so every server renders the same diffID for the same binary.
var shellLayers sync.Map

// ShellLayer returns the zstd-compressed shell layer for arch and its diffID.
func ShellLayer(arch db.Arch) ([]byte, string, error) {
	if l, ok := shellLayers.Load(arch); ok {
		layer := l.(*shellLayer)
		return layer.compressed, layer.diffID, nil
	}
	busybox, err := shellFS.ReadFile("shell/busybox-" + string(arch))
	if err != nil {
		return nil, "", fmt.Errorf("no shell is baked in for %s: %w", arch, err)
	}
	listed, err := shellFS.ReadFile("shell/applets-" + string(arch))
	if err != nil {
		return nil, "", fmt.Errorf("no applet list is baked in for %s: %w", arch, err)
	}
	layer, err := buildShellLayer(busybox, strings.Fields(string(listed)))
	if err != nil {
		return nil, "", err
	}
	shellLayers.Store(arch, layer)
	return layer.compressed, layer.diffID, nil
}

// buildShellLayer writes the deterministic shell layer: /bin/busybox and a
// relative symlink per applet, with the same pinned headers the essentials layer
// uses, so the diffID is the same on every server.
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
