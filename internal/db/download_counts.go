package db

import (
	"context"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

type PackagedFormat struct {
	Format   string
	Size     int64
	SHA256   string
	Filename string
}

type ArtifactDetail struct {
	model.Artifact
	DownloadCount int64
	Packages      []PackagedFormat
}

func (d *DB) IncrementDownloadCount(ctx context.Context, artifactID int64) error {
	return d.q.IncrementDownloadCount(ctx, artifactID)
}

func (d *DB) ListArtifactDetails(ctx context.Context, releaseID int64) ([]ArtifactDetail, error) {
	rows, err := d.q.ListArtifactDetailsWithDownloads(ctx, releaseID)
	if err != nil {
		return nil, fmt.Errorf("list artifact details: %w", err)
	}

	details := make([]ArtifactDetail, len(rows))
	for i, row := range rows {
		details[i] = ArtifactDetail{
			Artifact: model.Artifact{
				ID:                 row.ID,
				ReleaseID:          row.ReleaseID,
				OS:                 model.OS(row.Os),
				Arch:               model.Arch(row.Arch),
				Kind:               model.Kind(row.Kind),
				StorageKey:         row.StorageKey,
				Size:               row.Size,
				SHA256:             row.Sha256,
				StrippedStorageKey: row.StrippedStorageKey,
				StrippedSize:       row.StrippedSize,
				StrippedSHA256:     row.StrippedSha256,
				DebugStorageKey:    row.DebugStorageKey,
				DebugSize:          row.DebugSize,
				Filename:           row.Filename,
				CreatedAt:          row.CreatedAt,
			},
			DownloadCount: row.DownloadCount,
		}
	}

	for i, a := range details {
		pkgs, err := d.listPackagedFormats(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		details[i].Packages = pkgs
	}

	return details, nil
}

func (d *DB) listPackagedFormats(ctx context.Context, artifactID int64) ([]PackagedFormat, error) {
	rows, err := d.q.ListPackagedFormats(ctx, artifactID)
	if err != nil {
		return nil, fmt.Errorf("list packaged formats: %w", err)
	}
	pkgs := make([]PackagedFormat, len(rows))
	for i, row := range rows {
		pkgs[i] = PackagedFormat{
			Format:   row.Format,
			Size:     row.Size,
			SHA256:   row.Sha256,
			Filename: row.Filename,
		}
	}
	return pkgs, nil
}

func (d *DB) GetTotalDownloads(ctx context.Context, releaseID int64) (int64, error) {
	count, err := d.q.GetTotalDownloads(ctx, releaseID)
	if err != nil {
		return 0, err
	}
	return count, nil
}
