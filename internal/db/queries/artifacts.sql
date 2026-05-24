-- name: InsertArtifact :execresult
INSERT INTO artifacts (release_id, os, arch, kind, storage_key, size, sha256, filename)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateArtifactStripped :exec
UPDATE artifacts SET stripped_storage_key = ?, stripped_size = ?, stripped_sha256 = ?,
 debug_storage_key = ?, debug_size = ?
WHERE id = ?;

-- name: GetArtifactByReleaseOSArch :one
SELECT id, release_id, os, arch, kind, storage_key, size, sha256,
       stripped_storage_key, stripped_size, stripped_sha256,
       debug_storage_key, debug_size, filename, created_at
FROM artifacts WHERE release_id = ? AND os = ? AND arch = ?;

-- name: ListArtifactsByRelease :many
SELECT id, release_id, os, arch, kind, storage_key, size, sha256,
       stripped_storage_key, stripped_size, stripped_sha256,
       debug_storage_key, debug_size, filename, created_at
FROM artifacts WHERE release_id = ?;

-- name: UpsertPackagedArtifact :exec
INSERT OR REPLACE INTO packaged_artifacts (artifact_id, format, storage_key, size, sha256, filename, metadata)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetPackagedArtifact :one
SELECT storage_key, size, sha256, filename FROM packaged_artifacts
WHERE artifact_id = ? AND format = ?;

-- name: BlobBelongsToProject :one
SELECT EXISTS(
    SELECT 1 FROM artifacts a
    JOIN releases r ON a.release_id = r.id
    WHERE r.project_id = sqlc.arg(project_id)
    AND (a.storage_key = sqlc.arg(storage_key) OR a.stripped_storage_key = sqlc.arg(storage_key) OR a.debug_storage_key = sqlc.arg(storage_key))
    UNION ALL
    SELECT 1 FROM packaged_artifacts pa
    JOIN artifacts a ON pa.artifact_id = a.id
    JOIN releases r ON a.release_id = r.id
    WHERE r.project_id = sqlc.arg(project_id) AND pa.storage_key = sqlc.arg(storage_key)
) AS blob_exists;
