package repackage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

type OCI struct{}

func (o *OCI) Format() Format { return FormatOCI }

func (o *OCI) Applicable(a db.Artifact) bool {
	return a.Kind == db.KindBinary || a.Kind == db.KindArchive
}

func (o *OCI) Repackage(_ context.Context, input Input) (*Output, error) {
	manifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"size":      len(input.Data),
		},
		"layers": []map[string]any{
			{
				"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
				"size":      len(input.Data),
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
