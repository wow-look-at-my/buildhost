package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (d *DB) CreateArtifact(ctx context.Context, a *Artifact) error {
	res, err := d.q.InsertArtifact(ctx, InsertArtifactParams{
		ReleaseID:  a.ReleaseID,
		OS:         a.OS,
		Arch:       a.Arch,
		Kind:       a.Kind,
		StorageKey: a.StorageKey,
		Size:       a.Size,
		SHA256:     a.SHA256,
		Filename:   a.Filename,
	})
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
	return d.q.UpdateArtifactStripped(ctx, UpdateArtifactStrippedParams{
		ID:                 id,
		StrippedStorageKey: strippedKey,
		StrippedSize:       strippedSize,
		StrippedSHA256:     strippedSHA256,
		DebugStorageKey:    debugKey,
		DebugSize:          debugSize,
	})
}

func (d *DB) GetArtifact(ctx context.Context, releaseID int64, os, arch string) (*Artifact, error) {
	row, err := d.q.GetArtifactByReleaseOSArch(ctx, GetArtifactByReleaseOSArchParams{
		ReleaseID: releaseID,
		OS:        OS(os),
		Arch:      Arch(arch),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get artifact: %w", err)
	}
	return &row, nil
}

func (d *DB) ListArtifacts(ctx context.Context, releaseID int64) ([]Artifact, error) {
	return d.q.ListArtifactsByRelease(ctx, releaseID)
}

func (d *DB) CreatePackagedArtifact(ctx context.Context, artifactID int64, format, storageKey string, size int64, sha256, filename, metadata string) error {
	return d.q.UpsertPackagedArtifact(ctx, UpsertPackagedArtifactParams{
		ArtifactID: artifactID,
		Format:     format,
		StorageKey: storageKey,
		Size:       size,
		SHA256:     sha256,
		Filename:   filename,
		Metadata:   metadata,
	})
}

func (d *DB) GetPackagedArtifact(ctx context.Context, artifactID int64, format string) (storageKey string, size int64, sha256sum string, filename string, err error) {
	row, err := d.q.GetPackagedArtifact(ctx, GetPackagedArtifactParams{
		ArtifactID: artifactID,
		Format:     format,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", "", ErrNotFound
	}
	if err != nil {
		return "", 0, "", "", err
	}
	return row.StorageKey, row.Size, row.SHA256, row.Filename, nil
}

func (d *DB) BlobBelongsToProject(ctx context.Context, projectID int64, storageKey string) (bool, error) {
	exists, err := d.q.BlobBelongsToProject(ctx, BlobBelongsToProjectParams{
		ProjectID:  projectID,
		StorageKey: storageKey,
	})
	return exists != 0, err
}
