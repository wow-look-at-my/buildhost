-- name: InsertProject :execresult
INSERT INTO projects (name, description, homepage, license, is_private, versioning)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetProjectByName :one
SELECT id, name, description, homepage, license, is_private, versioning, default_branch, created_at, updated_at
FROM projects WHERE name = ?;

-- name: ListAllProjects :many
SELECT id, name, description, homepage, license, is_private, versioning, default_branch, created_at, updated_at
FROM projects ORDER BY name;

-- name: SetProjectVisibility :exec
UPDATE projects SET is_private = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: GetProjectDefaultBranch :one
SELECT default_branch FROM projects WHERE id = ?;

-- name: SetProjectDefaultBranch :exec
UPDATE projects SET default_branch = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;
