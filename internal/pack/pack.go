// Package pack shrinks a served executable with UPX, a separate program the
// image ships and the host may install. GPL stays with that program; nothing
// of it is linked into the server.
package pack

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/exeformat"
	"github.com/wow-look-at-my/buildhost/internal/scratch"
)

// TransformVersion identifies the bytes stripping and packing produce.
const TransformVersion = "pack-1"

// ImagePath is where the production image ships the packer.
const ImagePath = "/usr/local/lib/buildhost/upx"

// EnvOverride names a packer explicitly. It wins over the image path and PATH.
const EnvOverride = "BUILDHOST_UPX"

// ErrUnavailable reports that no packer is installed. The caller serves the stripped binary.
var ErrUnavailable = errors.New("pack: no upx executable")

// ErrNotSmaller reports that packing the input would not shrink it. The caller serves the bytes.
var ErrNotSmaller = errors.New("pack: packed output is not smaller")

// ErrFormatChanged reports that the packed output is no longer the container format that went in, which is what an APE must never be.
var ErrFormatChanged = errors.New("pack: packed output changed the executable format")

// Packer runs a UPX executable.
type Packer struct {
	dir string
}

// Default returns a packer that keeps its scratch files under the scratch root.
func Default() *Packer { return New(scratch.Root()) }

// New returns a packer that keeps its scratch files in dir.
func New(dir string) *Packer { return &Packer{dir: dir} }

// Available reports whether a packer can be resolved.
func (p *Packer) Available() bool {
	_, err := resolve()
	return err == nil
}

// resolve finds the packer: an explicitly configured one, the image's copy,
// then one on PATH.
func resolve() (string, error) {
	if configured := strings.TrimSpace(os.Getenv(EnvOverride)); configured != "" {
		if isExecutable(configured) {
			return configured, nil
		}
		return "", fmt.Errorf("%w: %s=%s", ErrUnavailable, EnvOverride, configured)
	}
	if isExecutable(ImagePath) {
		return ImagePath, nil
	}
	if found, err := exec.LookPath("upx"); err == nil {
		return found, nil
	}
	return "", ErrUnavailable
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// Pack writes a packed copy of src to dst. It reports packed=false, with a
// reason. This happens when the packer refuses the input or the result
// would not be a smaller copy of the same executable. dst is removed in
// that case, so a caller always falls back to bytes it already trusts.
func (p *Packer) Pack(src, dst string) (bool, error) {
	tool, err := resolve()
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

// run executes the packer, turning a refusal into an error with its reason.
func (p *Packer) run(tool string, args ...string) error {
	cmd := exec.Command(tool, args...)
	cmd.Dir = p.dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if len(out) > 512 {
		out = out[len(out)-512:]
	}
	return fmt.Errorf("%w: %s", err, bytes.TrimSpace(out))
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

func isELF(b []byte) bool {
	return len(b) >= 4 && bytes.Equal(b[:4], []byte{0x7f, 'E', 'L', 'F'})
}

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
