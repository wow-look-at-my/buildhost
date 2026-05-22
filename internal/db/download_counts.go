package db

import (
	"context"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

func (d *DB) IncrementDownloadCount(ctx context.Context, artifactID int64) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO download_counts (artifact_id, count) VALUES (?, 1)
		 ON CONFLICT(artifact_id) DO UPDATE SET count = count + 1`, artifactID)
	return err
}

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

func (d *DB) ListArtifactDetails(ctx context.Context, releaseID int64) ([]ArtifactDetail, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT a.id, a.release_id, a.os, a.arch, a.kind, a.storage_key, a.size, a.sha256,
		       a.stripped_storage_key, a.stripped_size, a.stripped_sha256,
		       a.debug_storage_key, a.debug_size, a.filename, a.created_at,
		       COALESCE(dc.count, 0)
		FROM artifacts a
		LEFT JOIN download_counts dc ON dc.artifact_id = a.id
		WHERE a.release_id = ?
		ORDER BY a.os, a.arch, a.kind`, releaseID)
	if err != nil {
		return nil, fmt.Errorf("list artifact details: %w", err)
	}
	defer rows.Close()

	var details []ArtifactDetail
	for rows.Next() {
		var d ArtifactDetail
		if err := rows.Scan(&d.ID, &d.ReleaseID, &d.OS, &d.Arch, &d.Kind,
			&d.StorageKey, &d.Size, &d.SHA256,
			&d.StrippedStorageKey, &d.StrippedSize, &d.StrippedSHA256,
			&d.DebugStorageKey, &d.DebugSize, &d.Filename, &d.CreatedAt,
			&d.DownloadCount); err != nil {
			return nil, fmt.Errorf("scan artifact detail: %w", err)
		}
		details = append(details, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
	rows, err := d.QueryContext(ctx,
		`SELECT format, size, sha256, filename FROM packaged_artifacts WHERE artifact_id = ? ORDER BY format`,
		artifactID)
	if err != nil {
		return nil, fmt.Errorf("list packaged formats: %w", err)
	}
	defer rows.Close()

	var pkgs []PackagedFormat
	for rows.Next() {
		var p PackagedFormat
		if err := rows.Scan(&p.Format, &p.Size, &p.SHA256, &p.Filename); err != nil {
			return nil, fmt.Errorf("scan packaged format: %w", err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, rows.Err()
}

func (d *DB) GetTotalDownloads(ctx context.Context, releaseID int64) (int64, error) {
	var count int64
	err := d.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(dc.count), 0)
		FROM download_counts dc
		JOIN artifacts a ON dc.artifact_id = a.id
		WHERE a.release_id = ?`, releaseID).Scan(&count)
	return count, err
}
