package repackage

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

func init() { Register(&TarGZ{}) }

type TarGZ struct{}

func (t *TarGZ) Format() Format { return FormatTarGZ }

func (t *TarGZ) Applicable(a db.Artifact) bool { return !a.Kind.ServedViaDockerOnly() }

func (t *TarGZ) Repackage(_ context.Context, input Input) (*Output, error) {
	mode := int64(0o644)
	if input.Artifact.Kind == db.KindBinary {
		mode = 0o755
	}

	r := streamPipe(func(w io.Writer) error {
		gw := gzip.NewWriter(w)
		tw := tar.NewWriter(gw)
		if err := tw.WriteHeader(&tar.Header{Name: input.Project.Name, Size: input.Size, Mode: mode}); err != nil {
			return err
		}
		if _, err := io.Copy(tw, input.Reader); err != nil {
			return err
		}
		if err := tw.Close(); err != nil {
			return err
		}
		return gw.Close()
	})

	filename := fmt.Sprintf("%s-%s-%s-%s.tar.gz", input.Project.Name, input.Release.Version, input.Artifact.OS, input.Artifact.Arch)
	return &Output{
		Reader:   r,
		Filename: filename,
		Size:     SizeUnknown,
	}, nil
}
