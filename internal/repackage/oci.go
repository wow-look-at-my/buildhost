package repackage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

type OCI struct{}

func (o *OCI) Format() Format { return FormatOCI }

func (o *OCI) Applicable(a model.Artifact) bool {
	return a.Kind == model.KindBinary || a.Kind == model.KindArchive
}

func (o *OCI) Repackage(_ context.Context, input Input) (*Output, error) {
	data, err := io.ReadAll(input.Binary)
	if err != nil {
		return nil, fmt.Errorf("read binary: %w", err)
	}

	manifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"size":      len(data),
		},
		"layers": []map[string]any{
			{
				"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
				"size":      len(data),
			},
		},
	}

	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}

	filename := fmt.Sprintf("%s-%s-%s-%s-oci.json", input.Project.Name, input.Release.Version, input.Artifact.OS, input.Artifact.Arch)
	return &Output{
		Reader:   bytes.NewReader(manifestJSON),
		Filename: filename,
		Size:     int64(len(manifestJSON)),
		Metadata: map[string]string{
			"os":   string(input.Artifact.OS),
			"arch": string(input.Artifact.Arch),
		},
	}, nil
}
