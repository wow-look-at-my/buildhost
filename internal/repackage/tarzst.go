package repackage

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
	"github.com/wow-look-at-my/buildhost/internal/model"
)

type TarZST struct{}

func (t *TarZST) Format() Format { return FormatTarZST }

func (t *TarZST) Applicable(_ model.Artifact) bool { return true }

func (t *TarZST) Repackage(_ context.Context, input Input) (*Output, error) {
	data, err := io.ReadAll(input.Binary)
	if err != nil {
		return nil, fmt.Errorf("read binary: %w", err)
	}

	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		return nil, fmt.Errorf("create zstd writer: %w", err)
	}
	tw := tar.NewWriter(zw)

	mode := int64(0o644)
	if input.Artifact.Kind == model.KindBinary {
		mode = 0o755
	}

	if err := tw.WriteHeader(&tar.Header{
		Name: input.Project.Name,
		Size: int64(len(data)),
		Mode: mode,
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(data); err != nil {
		return nil, err
	}
	tw.Close()
	zw.Close()

	filename := fmt.Sprintf("%s-%s-%s-%s.tar.zst", input.Project.Name, input.Release.Version, input.Artifact.OS, input.Artifact.Arch)
	return &Output{
		Reader:   &buf,
		Filename: filename,
		Size:     int64(buf.Len()),
	}, nil
}
