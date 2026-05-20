package repackage

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/ulikunitz/xz"
	"github.com/wow-look-at-my/buildhost/internal/model"
)

type TarXZ struct{}

func (t *TarXZ) Format() Format { return FormatTarXZ }

func (t *TarXZ) Applicable(_ model.Artifact) bool { return true }

func (t *TarXZ) Repackage(_ context.Context, input Input) (*Output, error) {
	size, err := inputSize(input.Binary)
	if err != nil {
		return nil, fmt.Errorf("get input size: %w", err)
	}

	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	if err != nil {
		return nil, fmt.Errorf("create xz writer: %w", err)
	}
	tw := tar.NewWriter(xw)

	mode := int64(0o644)
	if input.Artifact.Kind == model.KindBinary {
		mode = 0o755
	}

	if err := tw.WriteHeader(&tar.Header{
		Name: input.Project.Name,
		Size: size,
		Mode: mode,
	}); err != nil {
		return nil, err
	}
	if _, err := io.Copy(tw, input.Binary); err != nil {
		return nil, err
	}
	tw.Close()
	xw.Close()

	filename := fmt.Sprintf("%s-%s-%s-%s.tar.xz", input.Project.Name, input.Release.Version, input.Artifact.OS, input.Artifact.Arch)
	return &Output{
		Reader:   &buf,
		Filename: filename,
		Size:     int64(buf.Len()),
	}, nil
}
