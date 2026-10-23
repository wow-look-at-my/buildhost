-- name: GetCacheEntry :one
SELECT key, storage_key, size, created_at FROM cache_entries
WHERE repo_id = ? AND ref = ? AND key = ?;

-- name: FindCacheEntryByPrefix :one
-- The newest entry of the scope whose key starts with the prefix, as a
-- restore-keys entry matches in GitHub's cache.
SELECT key, storage_key, size, created_at FROM cache_entries
WHERE repo_id = sqlc.arg(repo_id) AND ref = sqlc.arg(ref)
  AND substr(key, 1, length(CAST(sqlc.arg(prefix) AS TEXT))) = CAST(sqlc.arg(prefix) AS TEXT)
ORDER BY created_at DESC, key DESC
LIMIT 1;

-- name: InsertCacheEntry :execrows
-- A key in one scope is written once. A second save of it changes nothing.
INSERT INTO cache_entries (repo_id, ref, key, repo_path, storage_key, size)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (repo_id, ref, key) DO NOTHING;

-- name: MarkCacheEntryRead :exec
UPDATE cache_entries SET reads = reads + 1, used_at = datetime('now')
WHERE repo_id = ? AND ref = ? AND key = ?;

-- name: ListCacheEntries :many
-- Every entry, least recently used first: the order the janitor evicts in.
SELECT repo_id, ref, key, repo_path, storage_key, size, created_at, used_at, reads
FROM cache_entries
ORDER BY used_at, created_at;

-- name: DeleteCacheEntry :exec
DELETE FROM cache_entries WHERE repo_id = ? AND ref = ? AND key = ?;
