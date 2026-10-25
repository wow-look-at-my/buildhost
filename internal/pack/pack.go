// Package pack shrinks a served executable with the UPX packer that ships
// inside the server binary. The production image is distroless, so the packer
// is never looked up on PATH.
package pack

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/klauspost/compress/zstd"
	"github.com/wow-look-at-my/buildhost/internal/exeformat"
	"github.com/wow-look-at-my/buildhost/internal/scratch"
)

//go:generate go run ./upxgen upx
//go:embed upx/*.zst
var assets embed.FS

// TransformVersion identifies the bytes stripping and packing produce.
const TransformVersion = "pack-1"

// Release is the UPX version embedded in the server.
const Release = "5.2.1"

// ErrUnavailable reports that no packer is embedded for this platform. The caller serves the stripped binary instead.
var ErrUnavailable = errors.New("pack: no packer for this platform")

// ErrNotSmaller reports that packing the input would not shrink it. The caller serves the bytes.
var ErrNotSmaller = errors.New("pack: packed output is not smaller")

// ErrFormatChanged reports that the packed output is no longer the container format that went in, which is what an APE must never be.
var ErrFormatChanged = errors.New("pack: packed output changed the executable format")

// Packer runs the embedded UPX binary.
type Packer struct {
	dir  string
	once sync.Once
	tool string
	err  error
}

// Default returns a packer that unpacks its tool under the scratch root.
func Default() *Packer {
	return New(filepath.Join(scratch.Root(), scratch.ToolDirName))
}

// New returns a packer that unpacks its tool into dir.
func New(dir string) *Packer { return &Packer{dir: dir} }

// AssetName is the embedded asset for a platform, if there is one.
func AssetName(goos, goarch string) string {
	return "upx/upx-" + goos + "-" + goarch + ".zst"
}

// Available reports whether a packer is embedded for this platform.
func (p *Packer) Available() bool {
	return HasAsset(runtime.GOOS, runtime.GOARCH)
}

// HasAsset reports whether a packer is embedded for a platform.
func HasAsset(goos, goarch string) bool {
	_, err := assets.ReadFile(AssetName(goos, goarch))
	return err == nil
}

// Pack writes a packed copy of src to dst. It reports packed=false, with a
// reason. This happens when the packer refuses the input or the result
// would not be a smaller copy of the same executable. dst is removed in
// that case, so a caller always falls back to bytes it already trusts.
func (p *Packer) Pack(src, dst string) (bool, error) {
	tool, err := p.Tool()
	if err != nil {
		return false, err
	}
	srcInfo, err := os.Stat(src)
	if err != nil {
		return false, err
	}
	if err := p.run(tool, "-q", "-o", dst, src); err != nil {
		os.Remove(dst)
		return false, err
	}
	outInfo, err := os.Stat(dst)
	if err != nil {
		return false, err
	}
	if outInfo.Size() >= srcInfo.Size() {
		os.Remove(dst)
		return false, ErrNotSmaller
	}
	if err := sameExecutable(src, dst); err != nil {
		os.Remove(dst)
		return false, err
	}
	if err := p.verify(tool, src, dst); err != nil {
		os.Remove(dst)
		return false, err
	}
	return true, nil
}

// verify unpacks the packed file and requires the result to be the input,
// byte for byte. That is the proof the served file still runs the same code.
func (p *Packer) verify(tool, src, packed string) error {
	out, err := os.CreateTemp(p.dir, "verify-*")
	if err != nil {
		return err
	}
	path := out.Name()
	out.Close()
	os.Remove(path)
	defer os.Remove(path)
	if err := p.run(tool, "-d", "-q", "-o", path, packed); err != nil {
		return fmt.Errorf("pack: unpacking the result failed: %w", err)
	}
	equal, err := sameContent(src, path)
	if err != nil {
		return err
	}
	if !equal {
		return errors.New("pack: unpacking the result did not reproduce the input")
	}
	return nil
}

// Tool unpacks the embedded packer for this platform once and returns its path.
func (p *Packer) Tool() (string, error) {
	p.once.Do(func() {
		p.tool, p.err = p.extract()
	})
	return p.tool, p.err
}

func (p *Packer) extract() (string, error) {
	name := AssetName(runtime.GOOS, runtime.GOARCH)
	packed, err := assets.ReadFile(name)
	if err != nil {
		return "", ErrUnavailable
	}
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(p.dir, "upx-"+Release+"-"+runtime.GOOS+"-"+runtime.GOARCH)
	if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() {
		return target, nil
	}
	dec, err := zstd.NewReader(bytes.NewReader(packed))
	if err != nil {
		return "", err
	}
	data, err := io.ReadAll(dec)
	dec.Close()
	if err != nil {
		return "", fmt.Errorf("pack: unpacking the embedded tool: %w", err)
	}
	tmp, err := os.CreateTemp(p.dir, "upx-incoming-*")
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Chmod(0o700); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return target, nil
}

// run executes the packer, turning a refusal into an error with its reason.
func (p *Packer) run(tool string, args ...string) error {
	cmd := exec.Command(tool, args...)
	cmd.Dir = p.dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	msg := bytes.TrimSpace(out)
	if len(msg) > 512 {
		msg = msg[len(msg)-512:]
	}
	return fmt.Errorf("%w: %s", err, msg)
}

// sameExecutable requires the packed file to still be the container format
// that went in: ELF stays ELF, and an APE stays an APE.
func sameExecutable(src, dst string) error {
	in, err := head(src)
	if err != nil {
		return err
	}
	out, err := head(dst)
	if err != nil {
		return err
	}
	if isELF(in) && !isELF(out) {
		return ErrFormatChanged
	}
	if format := exeformat.Detect(in); format != "" && exeformat.Detect(out) != format {
		return ErrFormatChanged
	}
	return nil
}

func isELF(b []byte) bool { return len(b) >= 4 && bytes.Equal(b[:4], []byte{0x7f, 'E', 'L', 'F'}) }

func head(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, exeformat.SniffLen)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:n], nil
}

// sameContent reports whether files hold identical bytes.
func sameContent(a, b string) (bool, error) {
	ha, err := digest(a)
	if err != nil {
		return false, err
	}
	hb, err := digest(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ha, hb), nil
}

func digest(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
