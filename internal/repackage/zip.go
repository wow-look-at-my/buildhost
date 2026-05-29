package repackage

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

func init() { Register(&Zip{}) }

type Zip struct{}

func (z *Zip) Format() Format { return FormatZip }

func (z *Zip) Applicable(a db.Artifact) bool { return !a.Kind.ServedViaDockerOnly() }

func (z *Zip) Repackage(_ context.Context, input Input) (*Output, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	name := input.Project.Name
	if input.Artifact.OS == db.OSWindows && input.Artifact.Kind == db.KindBinary {
		name += ".exe"
	}

	fh := &zip.FileHeader{Name: name}
	fh.SetMode(0o755)
	w, err := zw.CreateHeader(fh)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(input.Data); err != nil {
		return nil, err
	}
	zw.Close()

	filename := fmt.Sprintf("%s-%s-%s-%s.zip", input.Project.Name, input.Release.Version, input.Artifact.OS, input.Artifact.Arch)
	return &Output{
		Reader:   &buf,
		Filename: filename,
		Size:     int64(buf.Len()),
	}, nil
}
