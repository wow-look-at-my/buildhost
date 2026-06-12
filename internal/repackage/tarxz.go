package repackage

import (
	"archive/tar"
	"context"
	"fmt"
	"io"

	"github.com/ulikunitz/xz"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func init() { Register(&TarXZ{}) }

type TarXZ struct{}

func (t *TarXZ) Format() Format { return FormatTarXZ }

func (t *TarXZ) Applicable(a db.Artifact) bool { return !a.Kind.ServedViaDockerOnly() }

func (t *TarXZ) Repackage(_ context.Context, input Input) (*Output, error) {
	mode := int64(0o644)
	if input.Artifact.Kind == db.KindBinary {
		mode = 0o755
	}

	r := streamPipe(func(w io.Writer) error {
		xw, err := xz.NewWriter(w)
		if err != nil {
			return fmt.Errorf("create xz writer: %w", err)
		}
		tw := tar.NewWriter(xw)
		if err := tw.WriteHeader(&tar.Header{Name: input.Project.Name, Size: input.Size, Mode: mode}); err != nil {
			return err
		}
		if _, err := io.Copy(tw, input.Reader); err != nil {
			return err
		}
		if err := tw.Close(); err != nil {
			return err
		}
		return xw.Close()
	})

	filename := fmt.Sprintf("%s-%s-%s-%s.tar.xz", input.Project.Name, input.Release.Version, input.Artifact.OS, input.Artifact.Arch)
	return &Output{
		Reader:   r,
		Filename: filename,
		Size:     SizeUnknown,
	}, nil
}
