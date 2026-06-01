-- name: InsertOCIBlobLink :exec
INSERT OR IGNORE INTO oci_blob_links (project_id, storage_key, media_type, size, is_manifest)
VALUES (?, ?, ?, ?, ?);

-- name: GetOCIBlobLink :one
SELECT id, project_id, storage_key, media_type, size, is_manifest, created_at
FROM oci_blob_links WHERE project_id = ? AND storage_key = ?;

-- name: UpsertOCITag :exec
INSERT INTO oci_tags (project_id, tag, manifest_digest, release_id, updated_at)
VALUES (?, ?, ?, ?, datetime('now'))
ON CONFLICT(project_id, tag) DO UPDATE SET
  manifest_digest = excluded.manifest_digest,
  release_id = excluded.release_id,
  updated_at = datetime('now');

-- name: GetOCITag :one
SELECT id, project_id, tag, manifest_digest, release_id, updated_at
FROM oci_tags WHERE project_id = ? AND tag = ?;

-- name: ListOCITags :many
SELECT id, project_id, tag, manifest_digest, release_id, updated_at
FROM oci_tags WHERE project_id = ? ORDER BY tag;
