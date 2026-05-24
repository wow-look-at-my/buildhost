package repackage

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

type OCI struct {
	Store storage.Storage
	DB    *db.DB
}

func (o *OCI) Format() Format { return FormatOCI }

func (o *OCI) Applicable(a model.Artifact) bool {
	return a.Kind == model.KindBinary
}

func (o *OCI) Repackage(ctx context.Context, input Input) (*Output, error) {
	if input.Artifact.OS == "" || input.Artifact.Arch == "" {
		return nil, fmt.Errorf("artifact missing os/arch")
	}

	layerData, diffID, err := ociCreateLayer(input.Data, input.Project.Name)
	if err != nil {
		return nil, fmt.Errorf("create layer: %w", err)
	}

	layerKey, layerSize, err := o.Store.Put(ctx, bytes.NewReader(layerData))
	if err != nil {
		return nil, fmt.Errorf("store layer: %w", err)
	}
	if input.Artifact.ID > 0 && o.DB != nil {
		o.DB.CreatePackagedArtifact(ctx, input.Artifact.ID, "oci-layer", layerKey, layerSize, layerKey, "layer.tar.zst", "{}")
	}

	configData := ociCreateConfig(string(input.Artifact.OS), string(input.Artifact.Arch), diffID, input.Project.Name)

	configKey, configSize, err := o.Store.Put(ctx, bytes.NewReader(configData))
	if err != nil {
		return nil, fmt.Errorf("store config: %w", err)
	}
	if input.Artifact.ID > 0 && o.DB != nil {
		o.DB.CreatePackagedArtifact(ctx, input.Artifact.ID, "oci-config", configKey, configSize, configKey, "config.json", "{}")
	}

	manifestData := ociCreateManifest(configKey, configSize, layerKey, layerSize)

	filename := fmt.Sprintf("%s-%s-%s-%s-oci-manifest.json", input.Project.Name, input.Release.Version, input.Artifact.OS, input.Artifact.Arch)
	return &Output{
		Reader:   bytes.NewReader(manifestData),
		Filename: filename,
		Size:     int64(len(manifestData)),
		Metadata: map[string]string{
			"os":   string(input.Artifact.OS),
			"arch": string(input.Artifact.Arch),
		},
	}, nil
}

func ociCreateLayer(data []byte, name string) (compressed []byte, diffID string, err error) {
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		return nil, "", fmt.Errorf("create zstd writer: %w", err)
	}
	tarHasher := sha256.New()

	tw := tar.NewWriter(io.MultiWriter(tarHasher, zw))
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Size:     int64(len(data)),
		Mode:     0o755,
		Typeflag: tar.TypeReg,
	}); err != nil {
		return nil, "", err
	}
	if _, err := tw.Write(data); err != nil {
		return nil, "", err
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}

	diffID = hex.EncodeToString(tarHasher.Sum(nil))
	return buf.Bytes(), diffID, nil
}

func ociCreateConfig(os, arch, diffID, name string) []byte {
	config := map[string]any{
		"architecture": arch,
		"os":           os,
		"rootfs": map[string]any{
			"type":     "layers",
			"diff_ids": []string{"sha256:" + diffID},
		},
		"config": map[string]any{
			"Entrypoint": []string{"/" + name},
		},
	}
	data, _ := json.Marshal(config)
	return data
}

func ociCreateManifest(configKey string, configSize int64, layerKey string, layerSize int64) []byte {
	manifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest":    "sha256:" + configKey,
			"size":      configSize,
		},
		"layers": []map[string]any{
			{
				"mediaType": "application/vnd.oci.image.layer.v1.tar+zstd",
				"digest":    "sha256:" + layerKey,
				"size":      layerSize,
			},
		},
	}
	data, _ := json.Marshal(manifest)
	return data
}
