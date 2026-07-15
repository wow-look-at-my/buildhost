package repackage

import (
	"archive/zip"
	"context"
	"fmt"
	"io"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

func init() { Register(&Zip{}) }

type Zip struct{}

func (z *Zip) Format() Format { return FormatZip }

func (z *Zip) Applicable(a db.Artifact) bool { return !a.Kind.ServedViaDockerOnly() }

func (z *Zip) Repackage(_ context.Context, input Input) (*Output, error) {
	name := input.Project.Name
	if input.Artifact.OS == db.OSWindows && input.Artifact.Kind == db.KindBinary {
		name += ".exe"
	}

	r := streamPipe(func(w io.Writer) error {
		zw := zip.NewWriter(w)
		fh := &zip.FileHeader{Name: name}
		fh.SetMode(0o755)
		fw, err := zw.CreateHeader(fh)
		if err != nil {
			return err
		}
		if _, err := io.Copy(fw, input.Reader); err != nil {
			return err
		}
		return zw.Close()
	})

	filename := fmt.Sprintf("%s-%s-%s-%s.zip", input.Project.Name, input.Release.Version, input.Artifact.OS, input.Artifact.Arch)
	return &Output{
		Reader:   r,
		Filename: filename,
		Size:     SizeUnknown,
	}, nil
}
