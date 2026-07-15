package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// LinkOCIBlob associates a pushed OCI blob or manifest with a project so the
// OCI download path (which gates on BlobBelongsToProject) will serve it. It is
// idempotent: re-linking the same (project, storage_key) is a no-op.
func (d *DB) LinkOCIBlob(ctx context.Context, projectID int64, storageKey, mediaType string, size int64, isManifest bool) error {
	var manifestFlag int64
	if isManifest {
		manifestFlag = 1
	}
	return d.q.InsertOCIBlobLink(ctx, InsertOCIBlobLinkParams{
		ProjectID:  projectID,
		StorageKey: storageKey,
		MediaType:  mediaType,
		Size:       size,
		IsManifest: manifestFlag,
	})
}

// GetOCIBlobLink returns the link row for a stored blob/manifest, or ErrNotFound.
func (d *DB) GetOCIBlobLink(ctx context.Context, projectID int64, storageKey string) (*OciBlobLink, error) {
	row, err := d.q.GetOCIBlobLink(ctx, GetOCIBlobLinkParams{
		ProjectID:  projectID,
		StorageKey: storageKey,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get oci blob link: %w", err)
	}
	return &row, nil
}

// SetOCITag points a docker tag at a manifest digest + release, creating or
// updating the tag (tags are mutable pointers).
func (d *DB) SetOCITag(ctx context.Context, projectID int64, tag, manifestDigest string, releaseID int64) error {
	return d.q.UpsertOCITag(ctx, UpsertOCITagParams{
		ProjectID:      projectID,
		Tag:            tag,
		ManifestDigest: manifestDigest,
		ReleaseID:      releaseID,
	})
}

// GetOCITag resolves a docker tag to its manifest digest + release, or ErrNotFound.
func (d *DB) GetOCITag(ctx context.Context, projectID int64, tag string) (*OciTag, error) {
	row, err := d.q.GetOCITag(ctx, GetOCITagParams{
		ProjectID: projectID,
		Tag:       tag,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get oci tag: %w", err)
	}
	return &row, nil
}

// ListOCITags returns all docker tags pushed to a project.
func (d *DB) ListOCITags(ctx context.Context, projectID int64) ([]OciTag, error) {
	return d.q.ListOCITags(ctx, projectID)
}
