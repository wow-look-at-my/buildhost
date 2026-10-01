-- name: InsertArtifact :execresult
INSERT INTO artifacts (release_id, os, arch, kind, storage_key, size, sha256, filename, exe_format)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: InsertArtifactPlatform :exec
INSERT INTO artifact_platforms (artifact_id, release_id, kind, os, arch, ordinal)
VALUES (?, ?, ?, ?, ?, ?);

-- name: UpdateArtifactStripped :exec
UPDATE artifacts SET stripped_storage_key = ?, stripped_size = ?, stripped_sha256 = ?,
 debug_storage_key = ?, debug_size = ?
WHERE id = ?;

-- Resolution goes through artifact_platforms, which holds every slot an
-- artifact occupies -- one row for an ordinary per-platform upload, N for a
-- multi-platform one. Every covered platform therefore resolves to the same
-- artifact, hence the same blob, digest and ETag.
-- name: GetArtifactByReleaseOSArch :one
SELECT a.id, a.release_id, a.os, a.arch, a.kind, a.storage_key, a.size, a.sha256,
       a.stripped_storage_key, a.stripped_size, a.stripped_sha256,
       a.debug_storage_key, a.debug_size, a.filename, a.exe_format, a.created_at
FROM artifacts a
JOIN artifact_platforms p ON p.artifact_id = a.id
WHERE p.release_id = ? AND p.os = ? AND p.arch = ?
ORDER BY a.id;

-- name: GetArtifactByReleaseAndKind :one
SELECT id, release_id, os, arch, kind, storage_key, size, sha256,
       stripped_storage_key, stripped_size, stripped_sha256,
       debug_storage_key, debug_size, filename, exe_format, created_at
FROM artifacts WHERE release_id = ? AND kind = ?;

-- name: ListArtifactsByRelease :many
SELECT id, release_id, os, arch, kind, storage_key, size, sha256,
       stripped_storage_key, stripped_size, stripped_sha256,
       debug_storage_key, debug_size, filename, exe_format, created_at
FROM artifacts WHERE release_id = ?;

-- name: ListArtifactPlatformsByRelease :many
SELECT artifact_id, os, arch FROM artifact_platforms
WHERE release_id = ? ORDER BY artifact_id, ordinal;

-- name: ListArtifactPlatformsByArtifact :many
SELECT os, arch FROM artifact_platforms WHERE artifact_id = ? ORDER BY ordinal;

-- name: DeleteReleaseArtifactPlatforms :exec
DELETE FROM artifact_platforms WHERE release_id = ?;

-- name: UpsertPackagedArtifact :exec
INSERT OR REPLACE INTO packaged_artifacts (artifact_id, format, storage_key, size, sha256, filename, metadata)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetPackagedArtifact :one
SELECT storage_key, size, sha256, filename, metadata FROM packaged_artifacts
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
    UNION ALL
    SELECT 1 FROM oci_blob_links obl
    WHERE obl.project_id = sqlc.arg(project_id) AND obl.storage_key = sqlc.arg(storage_key)
) AS blob_exists;
