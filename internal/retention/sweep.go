// Package retention implements buildhost's eviction policy and reference-counted
// garbage collection. Policy (keep-N per branch, abandoned-upload sweep) decides
// which releases to forget; the GC sweep decides when a content-addressed blob is
// safe to delete. It is the single source of truth shared by the background
// sweeper, the `buildhost gc` CLI, and the admin reclaimable-bytes estimate.
//
// Storage is content-addressed and deduplicated, so a blob freed by one deletion
// may still back another artifact, site, or OCI image. Blobs are therefore never
// deleted by ownership -- only when a global reference check finds none remaining.
package retention

import (
	"context"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// DeleteBlobIfUnreferenced deletes a content-addressed blob from storage only if
// no row in any project still references it. On a reference-check error it keeps
// the blob (fail safe -- a leaked blob is recoverable, a wrongly-deleted shared
// blob is not). When enforce is false it performs the check but does not delete,
// reporting whether the blob WOULD be deleted. Returns (deleted-or-would-delete,
// err).
//
// This is also the safe replacement for the sites delete and re-upload paths,
// which previously called Store.Delete unconditionally and could remove a
// dedup-shared blob.
func DeleteBlobIfUnreferenced(ctx context.Context, database *db.DB, store storage.Storage, key string, enforce bool) (bool, error) {
	if key == "" {
		return false, nil
	}
	referenced, err := database.IsBlobReferenced(ctx, key)
	if err != nil {
		return false, fmt.Errorf("check blob references: %w", err)
	}
	if referenced {
		return false, nil
	}
	if enforce {
		if err := store.Delete(ctx, key); err != nil {
			return false, fmt.Errorf("delete blob %s: %w", key, err)
		}
	}
	return true, nil
}
