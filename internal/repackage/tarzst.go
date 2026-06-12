package repackage

import (
	"archive/tar"
	"context"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func init() { Register(&TarZST{}) }

type TarZST struct{}

func (t *TarZST) Format() Format { return FormatTarZST }

func (t *TarZST) Applicable(a db.Artifact) bool { return !a.Kind.ServedViaDockerOnly() }

func (t *TarZST) Repackage(_ context.Context, input Input) (*Output, error) {
	mode := int64(0o644)
	if input.Artifact.Kind == db.KindBinary {
		mode = 0o755
	}

	r := streamPipe(func(w io.Writer) error {
		zw, err := zstd.NewWriter(w)
		if err != nil {
			return fmt.Errorf("create zstd writer: %w", err)
		}
		tw := tar.NewWriter(zw)
		if err := tw.WriteHeader(&tar.Header{Name: input.Project.Name, Size: input.Size, Mode: mode}); err != nil {
			return err
		}
		if _, err := io.Copy(tw, input.Reader); err != nil {
			return err
		}
		if err := tw.Close(); err != nil {
			return err
		}
		return zw.Close()
	})

	filename := fmt.Sprintf("%s-%s-%s-%s.tar.zst", input.Project.Name, input.Release.Version, input.Artifact.OS, input.Artifact.Arch)
	return &Output{
		Reader:   r,
		Filename: filename,
		Size:     SizeUnknown,
	}, nil
}
