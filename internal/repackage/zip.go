package repackage

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

type Zip struct{}

func (z *Zip) Format() Format { return FormatZip }

func (z *Zip) Applicable(_ model.Artifact) bool { return true }

func (z *Zip) Repackage(_ context.Context, input Input) (*Output, error) {
	data, err := io.ReadAll(input.Binary)
	if err != nil {
		return nil, fmt.Errorf("read binary: %w", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	name := input.Project.Name
	if input.Artifact.OS == model.OSWindows && input.Artifact.Kind == model.KindBinary {
		name += ".exe"
	}

	fh := &zip.FileHeader{Name: name}
	fh.SetMode(0o755)
	w, err := zw.CreateHeader(fh)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
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
