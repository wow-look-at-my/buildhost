-- name: InsertRelease :execresult
INSERT INTO releases (project_id, version, version_num, git_branch, git_commit, notes, oci_user, draft)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetMaxVersionNum :one
-- An auto project shares one number line with every auto project of its family: "repo" and each "repo/<binary>".
-- A build numbers the release in "repo" and publishes "repo/<binary>" under that number, so the number must be new in both.
SELECT CAST(COALESCE(MAX(r.version_num), 0) AS INTEGER) AS max_version_num
FROM releases r
JOIN projects p ON p.id = r.project_id
JOIN (
    SELECT id, versioning,
        CASE WHEN instr(name, '/') > 0 THEN substr(name, 1, instr(name, '/') - 1) ELSE name END AS family
    FROM projects WHERE projects.id = ?
) self
WHERE p.id = self.id
    OR (self.versioning = 'auto' AND p.versioning = 'auto'
        AND (p.name = self.family OR substr(p.name, 1, length(self.family) + 1) = self.family || '/'));

-- name: GetReleaseByProjectAndVersion :one
SELECT id, project_id, version, version_num, git_branch, git_commit, notes, oci_user, published, draft, created_at, published_at
FROM releases WHERE project_id = ? AND version = ?;

-- name: GetLatestPublishedRelease :one
SELECT id, project_id, version, version_num, git_branch, git_commit, notes, oci_user, published, draft, created_at, published_at
FROM releases WHERE project_id = ? AND published = 1
ORDER BY version_num DESC LIMIT 1;

-- name: GetLatestPublishedReleaseByBranch :one
SELECT id, project_id, version, version_num, git_branch, git_commit, notes, oci_user, published, draft, created_at, published_at
FROM releases WHERE project_id = ? AND git_branch = ? AND published = 1
ORDER BY version_num DESC LIMIT 1;

-- name: GetLatestPublishedReleaseOnDefaultBranch :one
SELECT r.id, r.project_id, r.version, r.version_num, r.git_branch, r.git_commit, r.notes, r.oci_user, r.published, r.draft, r.created_at, r.published_at
FROM releases r
JOIN projects p ON p.id = r.project_id
WHERE r.project_id = ? AND r.git_branch = p.default_branch AND r.published = 1
ORDER BY r.version_num DESC LIMIT 1;

-- name: ListPublishedReleasesOnDefaultBranch :many
SELECT r.id, r.project_id, r.version, r.version_num, r.git_branch, r.git_commit, r.notes, r.oci_user, r.published, r.draft, r.created_at, r.published_at
FROM releases r
JOIN projects p ON p.id = r.project_id
WHERE r.project_id = ? AND r.git_branch = p.default_branch AND r.published = 1
ORDER BY r.version_num DESC;

-- name: ListReleasesByProject :many
SELECT id, project_id, version, version_num, git_branch, git_commit, notes, oci_user, published, draft, created_at, published_at
FROM releases WHERE project_id = ? ORDER BY version_num DESC;

-- name: PublishRelease :exec
-- Publishing clears the draft flag: a release cannot be both a private build
-- and part of the project's release stream.
UPDATE releases SET published = 1, draft = 0, published_at = datetime('now') WHERE id = ?;
