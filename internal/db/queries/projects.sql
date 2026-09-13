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

-- name: RenameProject :exec
UPDATE projects SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: ListProjectsByGitHubRepoID :many
SELECT id, name, description, homepage, license, is_private, versioning, github_repo, github_owner_id, github_repo_id, default_branch, create_service, created_at, updated_at
FROM projects WHERE github_repo_id = ? AND github_repo_id != '' ORDER BY name;

-- name: GetProjectByAlias :one
SELECT p.id, p.name, p.description, p.homepage, p.license, p.is_private, p.versioning, p.github_repo, p.github_owner_id, p.github_repo_id, p.default_branch, p.create_service, p.created_at, p.updated_at
FROM projects p JOIN project_aliases a ON a.project_id = p.id
WHERE a.name = ?;

-- name: InsertProjectAlias :exec
INSERT INTO project_aliases (name, project_id) VALUES (?, ?);

-- name: DeleteProjectAlias :exec
DELETE FROM project_aliases WHERE name = ?;

-- name: ListProjectAliases :many
SELECT name FROM project_aliases WHERE project_id = ? ORDER BY name;

-- name: ListAllProjectAliases :many
SELECT a.name, a.project_id, p.name AS project_name
FROM project_aliases a JOIN projects p ON p.id = a.project_id ORDER BY a.name;

-- name: CountProjectAliasByName :one
SELECT COUNT(*) FROM project_aliases WHERE name = ?;

-- name: ReassignProjectAliases :exec
UPDATE project_aliases SET project_id = ? WHERE project_id = ?;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = ?;

-- Merge reassignments. Every table carrying a project_id is repointed at the
-- surviving project inside one transaction. download_counts and download_events
-- hang off artifact_id, so they follow their releases with no statement here.

-- name: MaxReleaseVersionNum :one
SELECT CAST(COALESCE(MAX(version_num), 0) AS INTEGER) FROM releases WHERE project_id = ?;

-- name: ListReleasesForMerge :many
SELECT id, version, version_num FROM releases WHERE project_id = ? ORDER BY version_num;

-- name: MoveReleaseToProject :exec
UPDATE releases SET project_id = ?, version = ?, version_num = ? WHERE id = ?;

-- name: ReassignAPITokens :exec
UPDATE api_tokens SET project_id = ? WHERE project_id = ?;

-- name: ReassignOIDCPolicies :exec
UPDATE oidc_policies SET project_id = ? WHERE project_id = ?;

-- name: ReassignSites :exec
UPDATE sites SET project_id = ? WHERE project_id = ?;

-- name: ReassignOCIBlobLinks :exec
UPDATE oci_blob_links SET project_id = ? WHERE project_id = ?;

-- name: ReassignOCITags :exec
UPDATE oci_tags SET project_id = ? WHERE project_id = ?;

-- Site-branch and OCI-tag collisions are read through the existing
-- ListSitesByProject and ListOCITags queries in sites.sql and oci.sql.

-- name: ListOCIBlobKeys :many
SELECT storage_key FROM oci_blob_links WHERE project_id = ? ORDER BY storage_key;

-- name: DeleteOCIBlobLink :exec
DELETE FROM oci_blob_links WHERE project_id = ? AND storage_key = ?;
