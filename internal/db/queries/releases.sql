-- name: InsertRelease :execresult
INSERT INTO releases (project_id, version, version_num, git_branch, git_commit, notes, oci_user)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetMaxVersionNum :one
SELECT CAST(COALESCE(MAX(version_num), 0) AS INTEGER) AS max_version_num FROM releases WHERE project_id = ?;

-- name: GetReleaseByProjectAndVersion :one
SELECT id, project_id, version, version_num, git_branch, git_commit, notes, oci_user, published, created_at, published_at
FROM releases WHERE project_id = ? AND version = ?;

-- name: GetLatestPublishedRelease :one
SELECT id, project_id, version, version_num, git_branch, git_commit, notes, oci_user, published, created_at, published_at
FROM releases WHERE project_id = ? AND published = 1
ORDER BY version_num DESC LIMIT 1;

-- name: GetLatestPublishedReleaseByBranch :one
SELECT id, project_id, version, version_num, git_branch, git_commit, notes, oci_user, published, created_at, published_at
FROM releases WHERE project_id = ? AND git_branch = ? AND published = 1
ORDER BY version_num DESC LIMIT 1;

-- name: GetLatestPublishedReleaseOnDefaultBranch :one
SELECT r.id, r.project_id, r.version, r.version_num, r.git_branch, r.git_commit, r.notes, r.oci_user, r.published, r.created_at, r.published_at
FROM releases r
JOIN projects p ON p.id = r.project_id
WHERE r.project_id = ? AND r.git_branch = p.default_branch AND r.published = 1
ORDER BY r.version_num DESC LIMIT 1;

-- name: ListReleasesByProject :many
SELECT id, project_id, version, version_num, git_branch, git_commit, notes, oci_user, published, created_at, published_at
FROM releases WHERE project_id = ? ORDER BY version_num DESC;

-- name: PublishRelease :exec
UPDATE releases SET published = 1, published_at = datetime('now') WHERE id = ?;
