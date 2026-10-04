-- name: InsertRunLock :execrows
INSERT INTO run_locks (repo_id, run_id, run_attempt, name, value)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (repo_id, run_id, run_attempt, name) DO NOTHING;

-- name: GetRunLock :one
SELECT value FROM run_locks
WHERE repo_id = ? AND run_id = ? AND run_attempt = ? AND name = ?;

-- name: DeleteRunLocksBefore :execrows
DELETE FROM run_locks WHERE created_at < datetime(sqlc.arg(cutoff));
