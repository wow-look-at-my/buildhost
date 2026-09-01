package repackage

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

// Static busybox binaries used as the /bin/sh an APE image runs its binary
// through. Fetched (not committed) by scripts/fetch-busybox.sh -- the same
// generate-gate pattern as cacerts/ca-certificates.crt -- one per linux arch
// buildhost synthesizes an APE image for:
//
//	shell/busybox.linux.amd64  (x86_64  musl static-pie ELF)
//	shell/busybox.linux.arm64  (aarch64 musl static-pie ELF)
//
// //go:embed shell embeds whichever files are present at build time; the fetch
// script materializes them before `go-toolchain --generate <hash>`, so a fresh
// clone without generate fails to compile (pattern shell/.*: no matching files
// found), exactly like the embedded CA bundle.
//
//go:generate ../../scripts/fetch-busybox.sh shell
//go:embed shell
var shellBytes embed.FS

// apeShellApplets lists the busybox applets the embedded shell needs, as
// relative symlinks (bin/sh -> busybox, ...) alongside the binary in the image's
// /bin. The APE's own shell prologue shells out to a handful of tools to
// assimilate a native ELF on first run -- cksum, tr, mkdir, cp so far -- so the
// image must carry more than sh. It is a pinned, reviewed constant rather than a
// `busybox --list` walk at build time, so the layer stays fully deterministic
// and buildhost never has to execute a freshly downloaded binary.
var apeShellApplets = []string{"sh", "cksum", "tr", "mkdir", "cp"}

// shellFileName maps an os/arch pair to the embedded busybox file name. Only
// linux targets get an APE image (serveIndex filters to linux/*), so only the
// linux arches buildhost can synthesize are represented.
func shellFileName(os db.OS, arch db.Arch) string {
	if os != db.OSLinux {
		return ""
	}
	switch arch {
	case db.ArchAMD64:
		return "busybox.linux.amd64"
	case db.ArchARM64:
		return "busybox.linux.arm64"
	}
	return ""
}

// shellLayer is the built (compressed, diffID) busybox layer for one os/arch.
type shellLayer struct {
	compressed []byte
	diffID     string
}

// shellLayerCache memoizes the busybox layer per (os, arch). It is a small,
// fixed set of values (one per supported linux arch), so storing them all in a
// process-lifetime cache -- like essentialsLayer -- keeps repeated pulls cheap
// and the bytes identical across requests (required, since the pull path
// re-hashes everything per request).
var shellLayerCache sync.Map // map[string]shellLayer
var shellLayerErr sync.Map   // map[string]error

// busyboxLayer builds the deterministic, zstd-compressed busybox "shell" layer
// for an APE image: the static busybox at /bin/busybox plus relative applet
// symlinks (so /bin/sh exists without a second copy of the 1 MB binary). It is
// memoized per (os, arch): a build error is cached and returned to every caller
// rather than retried on each pull.
//
// The arch comes from the artifact's covered platform (each linux child names
// its own arch), so the binary's architecture always matches the image it is
// shipped in.
func busyboxLayer(os db.OS, arch db.Arch) (compressed []byte, diffID string, err error) {
	key := string(os) + "/" + string(arch)
	if v, ok := shellLayerCache.Load(key); ok {
		l := v.(shellLayer)
		return l.compressed, l.diffID, nil
	}
	if err, ok := shellLayerErr.Load(key); ok {
		return nil, "", err.(error)
	}

	compressed, diffID, err = buildShellLayer(os, arch)
	if err != nil {
		shellLayerErr.Store(key, err)
		return nil, "", err
	}
	shellLayerCache.Store(key, shellLayer{compressed, diffID})
	return compressed, diffID, nil
}

// buildShellLayer is the pure build for the busybox layer; busyboxLayer memoizes
// its result. It reads only the embedded busybox binaries and fixed literals and
// emits entries in a fixed order with pinned headers, so the output is
// byte-identical on every call.
func buildShellLayer(os db.OS, arch db.Arch) (compressed []byte, diffID string, err error) {
	name := "bin/" + shellFileName(os, arch)
	binData, err := shellBytes.ReadFile(name)
	if err != nil {
		return nil, "", fmt.Errorf("read embedded busybox %s: %w", name, err)
	}

	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, "", fmt.Errorf("create zstd writer: %w", err)
	}
	tarHasher := sha256.New()
	tw := tar.NewWriter(io.MultiWriter(tarHasher, zw))

	// bin/ directory, the busybox binary, then the applet symlinks (parents
	// before children; a fixed slice keeps it deterministic).
	binDir := "bin/"
	if err := writeTarEntry(tw, binDir, 0o755, tar.TypeDir, nil); err != nil {
		zw.Close()
		return nil, "", fmt.Errorf("write %s: %w", binDir, err)
	}
	if err := writeTarEntry(tw, "bin/busybox", 0o755, tar.TypeReg, binData); err != nil {
		zw.Close()
		return nil, "", fmt.Errorf("write bin/busybox: %w", err)
	}
	for _, applet := range apeShellApplets {
		// Relative symlink (bin/sh -> busybox), so the link survives wherever
		// the layer is extracted to -- same reasoning as the Dockerfile's busybox
		// symlink farm.
		if err := writeTarSymlink(tw, "bin/"+applet, "busybox"); err != nil {
			zw.Close()
			return nil, "", fmt.Errorf("write bin/%s symlink: %w", applet, err)
		}
	}

	if err := tw.Close(); err != nil {
		zw.Close()
		return nil, "", err
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), hex.EncodeToString(tarHasher.Sum(nil)), nil
}

// writeTarSymlink writes one pinned, reproducible symlink entry. It is spelled
// out (rather than folded into writeTarEntry) because a symlink's tar entry has
// no data bytes; the link target lives in the header's Linkname.
func writeTarSymlink(tw *tar.Writer, name, linkname string) error {
	hdr := &tar.Header{
		Name:     name,
		Linkname: linkname,
		Typeflag: tar.TypeSymlink,
		Format:   tar.FormatUSTAR,
		ModTime:  time.Unix(0, 0),
		Uid:      0,
		Gid:      0,
	}
	return tw.WriteHeader(hdr)
}
