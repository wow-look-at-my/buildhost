-- name: ApplyProjectPrivate :execrows
UPDATE projects SET is_private = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND is_private != ?;

-- name: ListVisibilityFolders :many
SELECT prefix, mode, updated_at FROM visibility_folders ORDER BY prefix;

-- name: UpsertVisibilityFolder :exec
INSERT INTO visibility_folders (prefix, mode, updated_at) VALUES (?, ?, datetime('now'))
ON CONFLICT(prefix) DO UPDATE SET mode = excluded.mode, updated_at = datetime('now');

-- name: DeleteVisibilityFolder :exec
DELETE FROM visibility_folders WHERE prefix = ?;

-- name: GetProjectVisibilityRow :one
SELECT project_id, mode, auto_private, updated_at FROM project_visibility WHERE project_id = ?;

-- name: ListProjectVisibilityRows :many
SELECT project_id, mode, auto_private, updated_at FROM project_visibility;

-- name: UpsertProjectVisibilityRow :exec
INSERT INTO project_visibility (project_id, mode, auto_private, updated_at) VALUES (?, ?, ?, datetime('now'))
ON CONFLICT(project_id) DO UPDATE SET mode = excluded.mode, auto_private = excluded.auto_private, updated_at = datetime('now');

-- name: GetReleaseVisibility :one
SELECT mode FROM release_visibility WHERE release_id = ?;

-- name: ListReleaseVisibilityByProject :many
SELECT rv.release_id, rv.mode FROM release_visibility rv
JOIN releases r ON r.id = rv.release_id
WHERE r.project_id = ?;

-- name: UpsertReleaseVisibility :exec
INSERT INTO release_visibility (release_id, mode, updated_at) VALUES (?, ?, datetime('now'))
ON CONFLICT(release_id) DO UPDATE SET mode = excluded.mode, updated_at = datetime('now');

-- name: DeleteReleaseVisibility :exec
DELETE FROM release_visibility WHERE release_id = ?;

-- name: GetSiteVisibility :one
SELECT mode FROM site_visibility WHERE project_id = ? AND branch = ?;

-- name: ListSiteVisibilityByProject :many
SELECT branch, mode FROM site_visibility WHERE project_id = ?;

-- name: UpsertSiteVisibility :exec
INSERT INTO site_visibility (project_id, branch, mode, updated_at) VALUES (?, ?, ?, datetime('now'))
ON CONFLICT(project_id, branch) DO UPDATE SET mode = excluded.mode, updated_at = datetime('now');

-- name: DeleteSiteVisibility :exec
DELETE FROM site_visibility WHERE project_id = ? AND branch = ?;
