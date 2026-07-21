-- name: InsertProject :execresult
INSERT INTO projects (name, description, homepage, license, is_private, versioning, github_repo, github_owner_id, github_repo_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetProjectByName :one
SELECT id, name, description, homepage, license, is_private, versioning, github_repo, github_owner_id, github_repo_id, default_branch, create_service, created_at, updated_at
FROM projects WHERE name = ?;

-- name: ListAllProjects :many
SELECT id, name, description, homepage, license, is_private, versioning, github_repo, github_owner_id, github_repo_id, default_branch, create_service, created_at, updated_at
FROM projects ORDER BY name;

-- name: SetProjectVisibility :exec
UPDATE projects SET is_private = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: SetProjectGitHubRepo :exec
UPDATE projects SET github_repo = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: SetProjectGitHubIDs :exec
UPDATE projects SET github_owner_id = ?, github_repo_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: SetProjectDefaultBranch :exec
UPDATE projects SET default_branch = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: SetProjectCreateService :exec
UPDATE projects SET create_service = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;
