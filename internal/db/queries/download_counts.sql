-- name: IncrementDownloadCount :exec
INSERT INTO download_counts (artifact_id, count) VALUES (?, 1)
ON CONFLICT(artifact_id) DO UPDATE SET count = count + 1;

-- name: ListArtifactDetailsWithDownloads :many
SELECT a.id, a.release_id, a.os, a.arch, a.kind, a.storage_key, a.size, a.sha256,
       a.stripped_storage_key, a.stripped_size, a.stripped_sha256,
       a.debug_storage_key, a.debug_size, a.filename, a.created_at,
       CAST(COALESCE(dc.count, 0) AS INTEGER) AS download_count
FROM artifacts a
LEFT JOIN download_counts dc ON dc.artifact_id = a.id
WHERE a.release_id = ?
ORDER BY a.os, a.arch, a.kind;

-- name: ListPackagedFormats :many
SELECT format, size, sha256, filename FROM packaged_artifacts
WHERE artifact_id = ? ORDER BY format;

-- name: GetTotalDownloads :one
SELECT CAST(COALESCE(SUM(dc.count), 0) AS INTEGER) AS total
FROM download_counts dc
JOIN artifacts a ON dc.artifact_id = a.id
WHERE a.release_id = ?;
