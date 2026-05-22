-- name: GetDashboardStats :one
SELECT
    (SELECT COUNT(*) FROM projects) AS project_count,
    (SELECT COUNT(*) FROM releases) AS release_count,
    (SELECT COUNT(*) FROM artifacts) AS artifact_count,
    CAST((SELECT COALESCE(SUM(size), 0) FROM artifacts) AS INTEGER) AS total_storage_bytes,
    (SELECT COUNT(*) FROM api_tokens) AS token_count,
    (SELECT COUNT(*) FROM oidc_policies) AS oidc_policy_count;

-- name: ListRecentReleases :many
SELECT r.id, r.project_id, r.version, r.version_num, r.git_branch, r.git_commit,
       r.notes, r.published, r.created_at, r.published_at, p.name AS project_name
FROM releases r
JOIN projects p ON r.project_id = p.id
ORDER BY r.created_at DESC, r.id DESC
LIMIT ?;

-- name: ListProjectSummaries :many
SELECT p.id, p.name, p.description, p.homepage, p.license, p.is_private, p.versioning,
       p.created_at, p.updated_at,
       (SELECT COUNT(*) FROM releases WHERE project_id = p.id) AS release_count,
       (SELECT COUNT(*) FROM artifacts a JOIN releases r ON a.release_id = r.id WHERE r.project_id = p.id) AS artifact_count
FROM projects p
ORDER BY p.name;

-- name: ListReleaseSummaries :many
SELECT r.id, r.project_id, r.version, r.version_num, r.git_branch, r.git_commit,
       r.notes, r.published, r.created_at, r.published_at,
       (SELECT COUNT(*) FROM artifacts WHERE release_id = r.id) AS artifact_count
FROM releases r
WHERE r.project_id = ?
ORDER BY r.version_num DESC;

-- name: ListTokenDetails :many
SELECT t.id, t.name, t.token_prefix, t.project_id, t.scopes, t.expires_at, t.created_at, t.last_used_at,
       COALESCE(p.name, '') AS project_name
FROM api_tokens t
LEFT JOIN projects p ON t.project_id = p.id
ORDER BY t.created_at DESC;

-- name: ListOIDCPolicyDetails :many
SELECT o.id, o.issuer, o.subject_pattern, o.audience, o.project_id, o.scopes, o.created_at,
       COALESCE(p.name, '') AS project_name
FROM oidc_policies o
LEFT JOIN projects p ON o.project_id = p.id
ORDER BY o.created_at DESC;
