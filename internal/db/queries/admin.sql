-- name: GetDashboardStats :one
SELECT
    (SELECT COUNT(*) FROM projects) AS project_count,
    (SELECT COUNT(*) FROM releases) AS release_count,
    (SELECT COUNT(*) FROM artifacts) AS artifact_count,
    CAST((SELECT COALESCE(SUM(size), 0) FROM artifacts) AS INTEGER) AS total_storage_bytes,
    (SELECT COUNT(*) FROM api_tokens) AS token_count,
    (SELECT COUNT(*) FROM oidc_policies) AS oidc_policy_count,
    (SELECT COUNT(*) FROM sites) AS site_count,
    CAST(
        (SELECT COALESCE(SUM(size), 0) FROM artifacts)
        + (SELECT COALESCE(SUM(CASE WHEN stripped_storage_key != '' THEN stripped_size ELSE 0 END), 0) FROM artifacts)
        + (SELECT COALESCE(SUM(CASE WHEN debug_storage_key != '' THEN debug_size ELSE 0 END), 0) FROM artifacts)
        + (SELECT COALESCE(SUM(size), 0) FROM packaged_artifacts)
    AS INTEGER) AS logical_bytes,
    CAST((SELECT COALESCE(SUM(sz), 0) FROM (
        SELECT k, MAX(sz) AS sz FROM (
            SELECT storage_key AS k, size AS sz FROM artifacts
            UNION ALL
            SELECT stripped_storage_key, stripped_size FROM artifacts WHERE stripped_storage_key != ''
            UNION ALL
            SELECT debug_storage_key, debug_size FROM artifacts WHERE debug_storage_key != ''
            UNION ALL
            SELECT storage_key, size FROM packaged_artifacts
        ) GROUP BY k
    )) AS INTEGER) AS physical_bytes,
    CAST((SELECT COALESCE(SUM(CASE WHEN stripped_storage_key != '' THEN stripped_size ELSE 0 END), 0) FROM artifacts) AS INTEGER) AS stripped_bytes,
    CAST((SELECT COALESCE(SUM(CASE WHEN debug_storage_key != '' THEN debug_size ELSE 0 END), 0) FROM artifacts) AS INTEGER) AS debug_bytes,
    CAST((SELECT COALESCE(SUM(size), 0) FROM packaged_artifacts) AS INTEGER) AS packaged_bytes;

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
       (SELECT COUNT(*) FROM artifacts a JOIN releases r ON a.release_id = r.id WHERE r.project_id = p.id) AS artifact_count,
       (SELECT COUNT(*) FROM sites WHERE project_id = p.id) AS site_count
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

-- name: ListSiteDetails :many
SELECT s.id, s.project_id, s.branch, s.storage_key, s.size, s.sha256,
       s.file_count, s.git_commit, s.created_at, s.updated_at,
       p.name AS project_name
FROM sites s
JOIN projects p ON s.project_id = p.id
ORDER BY s.updated_at DESC;

-- name: ListAllArtifacts :many
SELECT a.id, a.os, a.arch, a.kind, a.size, a.filename, a.created_at,
       r.version, r.git_branch,
       p.name AS project_name,
       CAST(COALESCE(dc.count, 0) AS INTEGER) AS download_count
FROM artifacts a
JOIN releases r ON a.release_id = r.id
JOIN projects p ON r.project_id = p.id
LEFT JOIN download_counts dc ON dc.artifact_id = a.id
ORDER BY a.created_at DESC, a.id DESC;

-- name: GetStorageBreakdown :many
SELECT p.id, p.name,
       CAST(COALESCE(SUM(a.size), 0) AS INTEGER) AS total_bytes,
       COUNT(a.id) AS artifact_count,
       COUNT(DISTINCT r.id) AS release_count
FROM projects p
LEFT JOIN releases r ON r.project_id = p.id
LEFT JOIN artifacts a ON a.release_id = r.id
GROUP BY p.id, p.name
ORDER BY total_bytes DESC;
