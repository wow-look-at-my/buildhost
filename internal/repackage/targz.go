package repackage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

type TarGZ struct{}

func (t *TarGZ) Format() Format { return FormatTarGZ }

func (t *TarGZ) Applicable(_ model.Artifact) bool { return true }

func (t *TarGZ) Repackage(_ context.Context, input Input) (*Output, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	mode := int64(0o644)
	if input.Artifact.Kind == model.KindBinary {
		mode = 0o755
	}

	if err := tw.WriteHeader(&tar.Header{
		Name: input.Project.Name,
		Size: int64(len(input.Data)),
		Mode: mode,
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(input.Data); err != nil {
		return nil, err
	}
	tw.Close()
	gw.Close()

	filename := fmt.Sprintf("%s-%s-%s-%s.tar.gz", input.Project.Name, input.Release.Version, input.Artifact.OS, input.Artifact.Arch)
	return &Output{
		Reader:   &buf,
		Filename: filename,
		Size:     int64(buf.Len()),
	}, nil
}
