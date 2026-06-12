-- name: GetSiteByProjectAndBranch :one
SELECT id, project_id, branch, storage_key, size, sha256, file_count, git_commit, is_public, created_at, updated_at
FROM sites WHERE project_id = ? AND branch = ?;

-- name: ListSitesByProject :many
SELECT id, project_id, branch, storage_key, size, sha256, file_count, git_commit, is_public, created_at, updated_at
FROM sites WHERE project_id = ? ORDER BY updated_at DESC;

-- name: UpsertSite :execresult
INSERT INTO sites (project_id, branch, storage_key, size, sha256, file_count, git_commit, is_public, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
ON CONFLICT(project_id, branch) DO UPDATE SET
  storage_key = excluded.storage_key,
  size = excluded.size,
  sha256 = excluded.sha256,
  file_count = excluded.file_count,
  git_commit = excluded.git_commit,
  is_public = excluded.is_public,
  updated_at = datetime('now');

-- name: GetSiteStorageKey :one
SELECT storage_key FROM sites WHERE project_id = ? AND branch = ?;

-- name: DeleteSiteByProjectAndBranch :exec
DELETE FROM sites WHERE project_id = ? AND branch = ?;
