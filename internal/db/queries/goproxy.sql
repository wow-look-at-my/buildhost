-- name: UpsertGoproxyModule :one
INSERT INTO goproxy_modules (module_path, source)
VALUES (?, ?)
ON CONFLICT(module_path) DO UPDATE SET source = excluded.source
RETURNING id;

-- name: MarkGoproxyModuleSuccess :exec
UPDATE goproxy_modules
SET last_success_at = datetime('now'), last_error_kind = '', last_error = '', last_error_at = NULL
WHERE id = ?;

-- name: MarkGoproxyModuleError :exec
UPDATE goproxy_modules
SET last_error_kind = ?, last_error = ?, last_error_at = datetime('now')
WHERE id = ?;

-- name: GetGoproxyVersion :one
SELECT v.id, v.module_id, v.version, v.commit_sha, v.committed_at, v.go_mod,
       v.zip_storage_key, v.zip_size, v.fetched_at
FROM goproxy_versions v
JOIN goproxy_modules m ON m.id = v.module_id
WHERE m.module_path = ? AND v.version = ?;

-- name: UpsertGoproxyVersion :exec
INSERT INTO goproxy_versions (module_id, version, commit_sha, committed_at, go_mod, zip_storage_key, zip_size)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(module_id, version) DO UPDATE SET
    commit_sha      = excluded.commit_sha,
    committed_at    = excluded.committed_at,
    go_mod          = excluded.go_mod,
    zip_storage_key = CASE WHEN excluded.zip_storage_key != '' THEN excluded.zip_storage_key ELSE goproxy_versions.zip_storage_key END,
    zip_size        = CASE WHEN excluded.zip_storage_key != '' THEN excluded.zip_size ELSE goproxy_versions.zip_size END,
    fetched_at      = datetime('now');

-- name: SetGoproxyVersionZip :exec
UPDATE goproxy_versions SET zip_storage_key = ?, zip_size = ?, fetched_at = datetime('now')
WHERE module_id = ? AND version = ?;

-- name: ListGoproxyModuleSummaries :many
-- One row per cached module for the admin dashboard: how much of it is cached,
-- when it last worked, and how it last failed.
SELECT m.module_path, m.source, m.last_error_kind, m.last_error, m.last_error_at, m.last_success_at,
       COUNT(v.id)                       AS version_count,
       COALESCE(SUM(v.zip_size), 0)      AS bytes,
       MAX(v.fetched_at)                 AS last_fetched_at
FROM goproxy_modules m
LEFT JOIN goproxy_versions v ON v.module_id = m.id
GROUP BY m.id
ORDER BY m.module_path;

-- name: ListGoproxyVersionsByModule :many
SELECT v.version, v.commit_sha, v.committed_at, v.zip_storage_key, v.zip_size, v.fetched_at
FROM goproxy_versions v
JOIN goproxy_modules m ON m.id = v.module_id
WHERE m.module_path = ?
ORDER BY v.fetched_at DESC;

-- name: GetGoproxyCacheStats :one
SELECT
    (SELECT COUNT(*) FROM goproxy_modules)                                   AS modules,
    (SELECT COUNT(*) FROM goproxy_versions)                                  AS versions,
    (SELECT COUNT(*) FROM goproxy_versions WHERE zip_storage_key != '')      AS zips,
    (SELECT COALESCE(SUM(zip_size), 0) FROM goproxy_versions)                AS bytes,
    (SELECT COUNT(*) FROM goproxy_modules WHERE last_error_kind != '')       AS failing_modules;
