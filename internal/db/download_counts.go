package db

import (
	"context"
	"fmt"
)

func (d *DB) IncrementDownloadCount(ctx context.Context, artifactID int64) error {
	return d.q.IncrementDownloadCount(ctx, artifactID)
}

func (d *DB) ListArtifactDetails(ctx context.Context, releaseID int64) ([]ListArtifactDetailsWithDownloadsRow, [][]ListPackagedFormatsRow, error) {
	rows, err := d.q.ListArtifactDetailsWithDownloads(ctx, releaseID)
	if err != nil {
		return nil, nil, fmt.Errorf("list artifact details: %w", err)
	}

	pkgs := make([][]ListPackagedFormatsRow, len(rows))
	for i, row := range rows {
		p, err := d.q.ListPackagedFormats(ctx, row.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("list packaged formats: %w", err)
		}
		pkgs[i] = p
	}
	return rows, pkgs, nil
}

func (d *DB) GetTotalDownloads(ctx context.Context, releaseID int64) (int64, error) {
	return d.q.GetTotalDownloads(ctx, releaseID)
}
