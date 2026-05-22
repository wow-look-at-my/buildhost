package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/db/dbgen"
	"github.com/wow-look-at-my/buildhost/internal/model"
)

func (d *DB) CreateArtifact(ctx context.Context, a *model.Artifact) error {
	res, err := d.q.InsertArtifact(ctx, dbgen.InsertArtifactParams{
		ReleaseID:  a.ReleaseID,
		Os:         string(a.OS),
		Arch:       string(a.Arch),
		Kind:       string(a.Kind),
		StorageKey: a.StorageKey,
		Size:       a.Size,
		Sha256:     a.SHA256,
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
	return d.q.UpdateArtifactStripped(ctx, dbgen.UpdateArtifactStrippedParams{
		ID:                 id,
		StrippedStorageKey: strippedKey,
		StrippedSize:       strippedSize,
		StrippedSha256:     strippedSHA256,
		DebugStorageKey:    debugKey,
		DebugSize:          debugSize,
	})
}

func (d *DB) GetArtifact(ctx context.Context, releaseID int64, os, arch string) (*model.Artifact, error) {
	row, err := d.q.GetArtifactByReleaseOSArch(ctx, dbgen.GetArtifactByReleaseOSArchParams{
		ReleaseID: releaseID,
		Os:        os,
		Arch:      arch,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get artifact: %w", err)
	}
	return artifactFromRow(row), nil
}

func (d *DB) ListArtifacts(ctx context.Context, releaseID int64) ([]model.Artifact, error) {
	rows, err := d.q.ListArtifactsByRelease(ctx, releaseID)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	artifacts := make([]model.Artifact, len(rows))
	for i, row := range rows {
		artifacts[i] = *artifactFromRow(row)
	}
	return artifacts, nil
}

func (d *DB) CreatePackagedArtifact(ctx context.Context, artifactID int64, format, storageKey string, size int64, sha256, filename, metadata string) error {
	return d.q.UpsertPackagedArtifact(ctx, dbgen.UpsertPackagedArtifactParams{
		ArtifactID: artifactID,
		Format:     format,
		StorageKey: storageKey,
		Size:       size,
		Sha256:     sha256,
		Filename:   filename,
		Metadata:   metadata,
	})
}

func (d *DB) GetPackagedArtifact(ctx context.Context, artifactID int64, format string) (storageKey string, size int64, sha256sum string, filename string, err error) {
	row, err := d.q.GetPackagedArtifact(ctx, dbgen.GetPackagedArtifactParams{
		ArtifactID: artifactID,
		Format:     format,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", "", ErrNotFound
	}
	if err != nil {
		return "", 0, "", "", err
	}
	return row.StorageKey, row.Size, row.Sha256, row.Filename, nil
}

func (d *DB) BlobBelongsToProject(ctx context.Context, projectID int64, storageKey string) (bool, error) {
	exists, err := d.q.BlobBelongsToProject(ctx, dbgen.BlobBelongsToProjectParams{
		ProjectID:  projectID,
		StorageKey: storageKey,
	})
	return exists != 0, err
}

func artifactFromRow(row dbgen.Artifact) *model.Artifact {
	return &model.Artifact{
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
	}
}
