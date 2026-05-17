package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

func (d *DB) CreateArtifact(ctx context.Context, a *model.Artifact) error {
	res, err := d.ExecContext(ctx,
		`INSERT INTO artifacts (release_id, os, arch, kind, storage_key, size, sha256, filename)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ReleaseID, a.OS, a.Arch, a.Kind, a.StorageKey, a.Size, a.SHA256, a.Filename)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("artifact %s/%s: %w", a.OS, a.Arch, ErrConflict)
		}
		return fmt.Errorf("insert artifact: %w", err)
	}
	id, _ := res.LastInsertId()
	a.ID = id
	return nil
}

func (d *DB) UpdateArtifactStripped(ctx context.Context, id int64, strippedKey string, strippedSize int64, strippedSHA256 string, debugKey string, debugSize int64) error {
	_, err := d.ExecContext(ctx,
		`UPDATE artifacts SET stripped_storage_key = ?, stripped_size = ?, stripped_sha256 = ?,
		 debug_storage_key = ?, debug_size = ?
		 WHERE id = ?`,
		strippedKey, strippedSize, strippedSHA256, debugKey, debugSize, id)
	return err
}

func (d *DB) GetArtifact(ctx context.Context, releaseID int64, os, arch string) (*model.Artifact, error) {
	a := &model.Artifact{}
	err := d.QueryRowContext(ctx,
		`SELECT id, release_id, os, arch, kind, storage_key, size, sha256,
		        stripped_storage_key, stripped_size, stripped_sha256,
		        debug_storage_key, debug_size, filename, created_at
		 FROM artifacts WHERE release_id = ? AND os = ? AND arch = ?`, releaseID, os, arch).Scan(
		&a.ID, &a.ReleaseID, &a.OS, &a.Arch, &a.Kind, &a.StorageKey, &a.Size, &a.SHA256,
		&a.StrippedStorageKey, &a.StrippedSize, &a.StrippedSHA256,
		&a.DebugStorageKey, &a.DebugSize, &a.Filename, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get artifact: %w", err)
	}
	return a, nil
}

func (d *DB) ListArtifacts(ctx context.Context, releaseID int64) ([]model.Artifact, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, release_id, os, arch, kind, storage_key, size, sha256,
		        stripped_storage_key, stripped_size, stripped_sha256,
		        debug_storage_key, debug_size, filename, created_at
		 FROM artifacts WHERE release_id = ?`, releaseID)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	defer rows.Close()

	var artifacts []model.Artifact
	for rows.Next() {
		var a model.Artifact
		if err := rows.Scan(&a.ID, &a.ReleaseID, &a.OS, &a.Arch, &a.Kind, &a.StorageKey, &a.Size, &a.SHA256,
			&a.StrippedStorageKey, &a.StrippedSize, &a.StrippedSHA256,
			&a.DebugStorageKey, &a.DebugSize, &a.Filename, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		artifacts = append(artifacts, a)
	}
	return artifacts, rows.Err()
}

func (d *DB) CreatePackagedArtifact(ctx context.Context, artifactID int64, format, storageKey string, size int64, sha256, filename, metadata string) error {
	_, err := d.ExecContext(ctx,
		`INSERT OR REPLACE INTO packaged_artifacts (artifact_id, format, storage_key, size, sha256, filename, metadata)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		artifactID, format, storageKey, size, sha256, filename, metadata)
	return err
}

func (d *DB) GetPackagedArtifact(ctx context.Context, artifactID int64, format string) (storageKey string, size int64, sha256sum string, filename string, err error) {
	err = d.QueryRowContext(ctx,
		`SELECT storage_key, size, sha256, filename FROM packaged_artifacts
		 WHERE artifact_id = ? AND format = ?`, artifactID, format).Scan(&storageKey, &size, &sha256sum, &filename)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", "", ErrNotFound
	}
	return
}
