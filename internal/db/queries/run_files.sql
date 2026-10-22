-- name: TouchRunStorageRun :exec
-- A use of a run makes it live again, so a re-run of a completed run keeps its
-- files until GitHub reports the re-run complete.
INSERT INTO run_storage_runs (repo_id, run_id, repo_path)
VALUES (?, ?, ?)
ON CONFLICT (repo_id, run_id) DO UPDATE SET
    repo_path  = excluded.repo_path,
    used_at    = datetime('now'),
    checked_at = NULL,
    ended_at   = NULL;

-- name: GetRunFile :one
SELECT storage_key, size FROM run_files
WHERE repo_id = ? AND run_id = ? AND name = ?;

-- name: UpsertRunFile :exec
INSERT INTO run_files (repo_id, run_id, name, storage_key, size)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (repo_id, run_id, name) DO UPDATE SET
    storage_key = excluded.storage_key,
    size        = excluded.size,
    created_at  = datetime('now');

-- name: ListRunFiles :many
SELECT name, storage_key, size, created_at, reads, read_at FROM run_files
WHERE repo_id = ? AND run_id = ?
ORDER BY name;

-- name: MarkRunFileRead :exec
UPDATE run_files SET reads = reads + 1, read_at = datetime('now')
WHERE repo_id = ? AND run_id = ? AND name = ?;

-- name: ListRunsToCheck :many
SELECT repo_id, run_id, repo_path FROM run_storage_runs
WHERE ended_at IS NULL AND (checked_at IS NULL OR checked_at < datetime(sqlc.arg(cutoff)))
ORDER BY used_at;

-- name: MarkRunChecked :exec
UPDATE run_storage_runs SET checked_at = datetime('now'), ended_at = sqlc.narg(ended_at)
WHERE repo_id = sqlc.arg(repo_id) AND run_id = sqlc.arg(run_id);

-- name: ListRunStorageRuns :many
SELECT r.repo_id, r.run_id, r.repo_path, r.created_at, r.used_at, r.ended_at, r.checked_at,
       CAST(COALESCE((SELECT SUM(f.size) FROM run_files f WHERE f.repo_id = r.repo_id AND f.run_id = r.run_id), 0) AS INTEGER) AS bytes
FROM run_storage_runs r
ORDER BY r.ended_at IS NULL, r.ended_at, r.created_at;

-- name: ListRunBlobFiles :many
SELECT f.repo_id, f.run_id, r.repo_path, f.name, f.storage_key, f.size, f.created_at
FROM run_files f
JOIN run_storage_runs r ON r.repo_id = f.repo_id AND r.run_id = f.run_id;

-- name: DeleteRunFiles :exec
DELETE FROM run_files WHERE repo_id = ? AND run_id = ?;

-- name: DeleteRunStorageRun :exec
DELETE FROM run_storage_runs WHERE repo_id = ? AND run_id = ?;
