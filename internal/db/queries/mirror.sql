-- name: GetMirrorBlob :one
SELECT upstream, digest, media_type, storage_key, size FROM mirror_blobs
WHERE upstream = ? AND digest = ?;

-- name: InsertMirrorBlob :exec
-- A digest names one content, so a second fetch of it changes nothing.
INSERT INTO mirror_blobs (upstream, digest, media_type, storage_key, size)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (upstream, digest) DO NOTHING;

-- name: MarkMirrorBlobRead :exec
UPDATE mirror_blobs SET reads = reads + 1, used_at = datetime('now')
WHERE upstream = ? AND digest = ?;

-- name: ListMirrorBlobs :many
-- Every blob, least recently used first: the order the janitor evicts in.
SELECT upstream, digest, media_type, storage_key, size, created_at, used_at, reads
FROM mirror_blobs
ORDER BY used_at, created_at;

-- name: DeleteMirrorBlob :exec
DELETE FROM mirror_blobs WHERE upstream = ? AND digest = ?;

-- name: GetMirrorTag :one
SELECT digest, media_type, checked_at FROM mirror_tags
WHERE upstream = ? AND repository = ? AND tag = ?;

-- name: UpsertMirrorTag :exec
INSERT INTO mirror_tags (upstream, repository, tag, digest, media_type)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (upstream, repository, tag) DO UPDATE SET
    digest     = excluded.digest,
    media_type = excluded.media_type,
    checked_at = datetime('now');

-- name: ListMirrorTags :many
SELECT upstream, repository, tag, digest, media_type, checked_at
FROM mirror_tags
ORDER BY upstream, repository, tag;

-- name: DeleteMirrorTagsForDigest :exec
-- A tag that names an evicted manifest gives a later pull nothing to use.
DELETE FROM mirror_tags WHERE upstream = ? AND digest = ?;
