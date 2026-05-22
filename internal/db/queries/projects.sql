-- name: InsertProject :execresult
INSERT INTO projects (name, description, homepage, license, is_private, versioning)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetProjectByName :one
SELECT id, name, description, homepage, license, is_private, versioning, created_at, updated_at
FROM projects WHERE name = ?;

-- name: ListAllProjects :many
SELECT id, name, description, homepage, license, is_private, versioning, created_at, updated_at
FROM projects ORDER BY name;
