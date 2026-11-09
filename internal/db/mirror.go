// MirrorTagState is the digest a tag named when the upstream last answered for it.

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MirrorTagState is the digest a tag named when the upstream last answered for it.
type MirrorTagState struct {
	Digest    string
	MediaType string
	CheckedAt time.Time
}

// GetMirrorBlob returns the blob the mirror holds for digest, and whether it holds one.
func (d *DB) GetMirrorBlob(ctx context.Context, upstream, digest string) (MirrorBlob, bool, error) {
	row, err := d.q.GetMirrorBlob(ctx, GetMirrorBlobParams{Upstream: upstream, Digest: digest})
	if errors.Is(err, sql.ErrNoRows) {
		return MirrorBlob{}, false, nil
	}
	if err != nil {
		return MirrorBlob{}, false, fmt.Errorf("get mirror blob: %w", err)
	}
	return MirrorBlob{Upstream: row.Upstream, Digest: row.Digest, MediaType: row.MediaType, StorageKey: row.StorageKey, Size: row.Size}, true, nil
}

// PutMirrorBlob records a blob the mirror stored. mediaType is empty for a layer or a config.
func (d *DB) PutMirrorBlob(ctx context.Context, upstream, digest, mediaType, storageKey string, size int64) error {
	err := d.q.InsertMirrorBlob(ctx, InsertMirrorBlobParams{
		Upstream: upstream, Digest: digest, MediaType: mediaType, StorageKey: storageKey, Size: size,
	})
	if err != nil {
		return fmt.Errorf("put mirror blob: %w", err)
	}
	return nil
}

// MarkMirrorBlobRead counts one read of the blob and moves its last use to now.
func (d *DB) MarkMirrorBlobRead(ctx context.Context, upstream, digest string) error {
	if err := d.q.MarkMirrorBlobRead(ctx, MarkMirrorBlobReadParams{Upstream: upstream, Digest: digest}); err != nil {
		return fmt.Errorf("mark mirror blob read: %w", err)
	}
	return nil
}

// ListMirrorBlobs returns every blob, least recently used first.
func (d *DB) ListMirrorBlobs(ctx context.Context) ([]MirrorBlob, error) {
	rows, err := d.q.ListMirrorBlobs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list mirror blobs: %w", err)
	}
	return rows, nil
}

// DeleteMirrorBlob forgets the blob and every tag and release asset that names
// it. The caller deletes the stored bytes once nothing else references them.
func (d *DB) DeleteMirrorBlob(ctx context.Context, upstream, digest string) error {
	if err := d.q.DeleteMirrorTagsForDigest(ctx, DeleteMirrorTagsForDigestParams{Upstream: upstream, Digest: digest}); err != nil {
		return fmt.Errorf("delete mirror tags: %w", err)
	}
	if err := d.q.DeleteMirrorReleaseAssetsForDigest(ctx, DeleteMirrorReleaseAssetsForDigestParams{Upstream: upstream, Digest: digest}); err != nil {
		return fmt.Errorf("delete mirror release assets: %w", err)
	}
	if err := d.q.DeleteMirrorBlob(ctx, DeleteMirrorBlobParams{Upstream: upstream, Digest: digest}); err != nil {
		return fmt.Errorf("delete mirror blob: %w", err)
	}
	return nil
}

// GetMirrorTag returns what the tag named at the last upstream answer, and whether the mirror knows the tag.
func (d *DB) GetMirrorTag(ctx context.Context, upstream, repository, tag string) (MirrorTagState, bool, error) {
	row, err := d.q.GetMirrorTag(ctx, GetMirrorTagParams{Upstream: upstream, Repository: repository, Tag: tag})
	if errors.Is(err, sql.ErrNoRows) {
		return MirrorTagState{}, false, nil
	}
	if err != nil {
		return MirrorTagState{}, false, fmt.Errorf("get mirror tag: %w", err)
	}
	return MirrorTagState{Digest: row.Digest, MediaType: row.MediaType, CheckedAt: row.CheckedAt}, true, nil
}

// PutMirrorTag records the digest the upstream named for the tag now.
func (d *DB) PutMirrorTag(ctx context.Context, upstream, repository, tag, digest, mediaType string) error {
	err := d.q.UpsertMirrorTag(ctx, UpsertMirrorTagParams{
		Upstream: upstream, Repository: repository, Tag: tag, Digest: digest, MediaType: mediaType,
	})
	if err != nil {
		return fmt.Errorf("put mirror tag: %w", err)
	}
	return nil
}

// ListMirrorTags returns every tag the mirror knows.
func (d *DB) ListMirrorTags(ctx context.Context) ([]MirrorTag, error) {
	rows, err := d.q.ListMirrorTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("list mirror tags: %w", err)
	}
	return rows, nil
}

// GetMirrorReleaseAsset returns the digest a release asset URL gave, and whether the mirror knows the asset.
func (d *DB) GetMirrorReleaseAsset(ctx context.Context, upstream, repository, tag, asset string) (string, bool, error) {
	digest, err := d.q.GetMirrorReleaseAsset(ctx, GetMirrorReleaseAssetParams{Upstream: upstream, Repository: repository, Tag: tag, Asset: asset})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get mirror release asset: %w", err)
	}
	return digest, true, nil
}

// PutMirrorReleaseAsset records the digest a release asset URL gave.
func (d *DB) PutMirrorReleaseAsset(ctx context.Context, upstream, repository, tag, asset, digest string) error {
	err := d.q.UpsertMirrorReleaseAsset(ctx, UpsertMirrorReleaseAssetParams{
		Upstream: upstream, Repository: repository, Tag: tag, Asset: asset, Digest: digest,
	})
	if err != nil {
		return fmt.Errorf("put mirror release asset: %w", err)
	}
	return nil
}

// ListMirrorReleaseAssets returns every release asset the mirror knows.
func (d *DB) ListMirrorReleaseAssets(ctx context.Context) ([]MirrorReleaseAsset, error) {
	rows, err := d.q.ListMirrorReleaseAssets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list mirror release assets: %w", err)
	}
	return rows, nil
}
