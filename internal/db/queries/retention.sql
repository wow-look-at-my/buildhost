-- name: GetRetentionSettings :one
SELECT keep_n, recency_hours FROM retention_settings WHERE id = 1;

-- name: SeedRetentionSettings :exec
INSERT OR IGNORE INTO retention_settings (id, keep_n, recency_hours) VALUES (1, ?, ?);

-- name: UpdateRetentionSettings :exec
INSERT INTO retention_settings (id, keep_n, recency_hours, updated_at)
VALUES (1, ?, ?, datetime('now'))
ON CONFLICT(id) DO UPDATE SET
    keep_n = excluded.keep_n,
    recency_hours = excluded.recency_hours,
    updated_at = datetime('now');

-- name: IsBlobReferenced :one
-- Global (un-scoped) generalization of BlobBelongsToProject: is this content
-- blob referenced by ANY row, in ANY project? Used by the GC sweep to decide
-- whether a freed candidate key is safe to delete from storage.
SELECT EXISTS(
    SELECT 1 FROM artifacts a
      WHERE a.storage_key = sqlc.arg(key) OR a.stripped_storage_key = sqlc.arg(key) OR a.debug_storage_key = sqlc.arg(key)
    UNION ALL SELECT 1 FROM packaged_artifacts pa WHERE pa.storage_key = sqlc.arg(key)
    UNION ALL SELECT 1 FROM sites s               WHERE s.storage_key = sqlc.arg(key)
    UNION ALL SELECT 1 FROM oci_blob_links obl    WHERE obl.storage_key = sqlc.arg(key)
    UNION ALL SELECT 1 FROM goproxy_versions gv   WHERE gv.zip_storage_key = sqlc.arg(key)
) AS referenced;

-- name: ListReleaseBlobKeys :many
-- Every (storage key, size) a release's artifacts (raw + stripped + debug) and
-- their packaged artifacts reference. Collected BEFORE the cascade delete so the
-- sweep can re-check each key for surviving references and attribute freed bytes.
SELECT a.storage_key AS k, a.size AS sz FROM artifacts a WHERE a.release_id = sqlc.arg(release_id) AND a.storage_key != ''
UNION
SELECT a.stripped_storage_key, a.stripped_size FROM artifacts a WHERE a.release_id = sqlc.arg(release_id) AND a.stripped_storage_key != ''
UNION
SELECT a.debug_storage_key, a.debug_size FROM artifacts a WHERE a.release_id = sqlc.arg(release_id) AND a.debug_storage_key != ''
UNION
SELECT pa.storage_key, pa.size FROM packaged_artifacts pa JOIN artifacts a ON pa.artifact_id = a.id
  WHERE a.release_id = sqlc.arg(release_id) AND pa.storage_key != '';

-- name: DeleteReleasePackagedArtifacts :exec
DELETE FROM packaged_artifacts WHERE artifact_id IN (SELECT id FROM artifacts WHERE release_id = ?);

-- name: DeleteReleaseDownloadCounts :exec
DELETE FROM download_counts WHERE artifact_id IN (SELECT id FROM artifacts WHERE release_id = ?);

-- name: DeleteReleaseOCITags :exec
DELETE FROM oci_tags WHERE release_id = ?;

-- name: DeleteReleaseArtifacts :exec
DELETE FROM artifacts WHERE release_id = ?;

-- name: DeleteReleaseRow :exec
DELETE FROM releases WHERE id = ?;

-- name: ListEvictableReleases :many
-- Published releases past keep-N on each (project, git_branch). A release is
-- evictable when at least max(keep_n, 1) NEWER published releases exist on the
-- same branch. The max(..., 1) floor means the per-branch tip (zero newer) is
-- ALWAYS kept, even if keep_n is set to 0 -- so eviction can never remove a
-- branch's latest published build (which /dl/.../branch/... resolves). Also
-- excludes anything newer than the recency cutoff, tagged releases, and
-- pushed-docker releases (their blobs live in project-scoped oci_blob_links, not
-- release-cascade-able). Correlated-subquery form (sqlc's SQLite analyzer does
-- not support window-fn aliases in WHERE).
SELECT r.id, r.project_id, p.name AS project_name, r.git_branch, r.version, r.version_num
FROM releases r
JOIN projects p ON p.id = r.project_id
WHERE r.published = 1
  AND r.created_at < datetime(sqlc.arg(recency_cutoff))
  AND r.id NOT IN (SELECT release_id FROM oci_tags)
  AND r.id NOT IN (SELECT release_id FROM artifacts WHERE kind = 'docker')
  AND (
      SELECT COUNT(*) FROM releases r2
      WHERE r2.project_id = r.project_id
        AND r2.git_branch = r.git_branch
        AND r2.published = 1
        AND r2.version_num > r.version_num
  ) >= max(sqlc.arg(keep_n), 1)
ORDER BY r.project_id, r.git_branch, r.version_num DESC;

-- name: ListAbandonedReleases :many
-- Unpublished (partial/failed upload) releases older than the cutoff.
--
-- Drafts are deliberately excluded: an unpublished release used to mean only
-- "an upload that never finished", so sweeping them was safe. A draft is an
-- unpublished release its owner MEANT to keep (downloadable by exact version,
-- invisible to latest/brew/apt/npm/OCI), and deleting one because it got old
-- would be deleting the feature.
SELECT r.id, r.project_id, p.name AS project_name, r.git_branch, r.version
FROM releases r
JOIN projects p ON p.id = r.project_id
WHERE r.published = 0 AND r.draft = 0 AND r.created_at < datetime(sqlc.arg(cutoff));

-- name: SumReclaimableBytes :one
-- UPPER BOUND on bytes keep-N would free: the logical sizes of evictable releases'
-- artifacts (raw+stripped+debug) plus their packaged artifacts. Does not subtract
-- blobs still shared with surviving releases, so it overestimates; the gc CLI and
-- sweeper report the exact post-refcount figure.
WITH evictable AS (
    SELECT r.id FROM releases r
    WHERE r.published = 1
      AND r.created_at < datetime(sqlc.arg(recency_cutoff))
      AND r.id NOT IN (SELECT release_id FROM oci_tags)
      AND r.id NOT IN (SELECT release_id FROM artifacts WHERE kind = 'docker')
      AND (
          SELECT COUNT(*) FROM releases r2
          WHERE r2.project_id = r.project_id
            AND r2.git_branch = r.git_branch
            AND r2.published = 1
            AND r2.version_num > r.version_num
      ) >= max(sqlc.arg(keep_n), 1)
)
SELECT CAST(
    COALESCE((SELECT SUM(a.size + a.stripped_size + a.debug_size)
              FROM artifacts a WHERE a.release_id IN (SELECT id FROM evictable)), 0)
  + COALESCE((SELECT SUM(pa.size) FROM packaged_artifacts pa
              JOIN artifacts a ON pa.artifact_id = a.id
              WHERE a.release_id IN (SELECT id FROM evictable)), 0)
AS INTEGER) AS reclaimable_bytes;

-- name: ListArtifactFiles :many
-- Every artifact row with the up to three blobs it references (raw, stripped,
-- debug) and the release/project context needed to explain why it is kept.
-- Feeds the retention inventory; the caller expands one row into one entry per
-- non-empty storage key.
SELECT a.id, a.release_id, a.os, a.arch, a.kind, a.filename, a.exe_format,
       a.storage_key, a.size, a.sha256,
       a.stripped_storage_key, a.stripped_size, a.stripped_sha256,
       a.debug_storage_key, a.debug_size,
       a.created_at,
       r.version, r.version_num, r.git_branch, r.published, r.draft,
       r.project_id, p.name AS project_name
FROM artifacts a
JOIN releases r ON r.id = a.release_id
JOIN projects p ON p.id = r.project_id
ORDER BY p.name, r.version_num, a.id;

-- name: ListPackagedFiles :many
-- Every stored repackaged artifact (the OCI layers; other formats stream and
-- are never stored) with its artifact, release and project context.
SELECT pa.id, pa.artifact_id, pa.format, pa.storage_key, pa.size, pa.sha256,
       pa.filename, pa.created_at,
       a.release_id, a.os, a.arch, a.kind,
       r.version, r.version_num, r.git_branch, r.published, r.draft,
       r.project_id, p.name AS project_name
FROM packaged_artifacts pa
JOIN artifacts a ON a.id = pa.artifact_id
JOIN releases r ON r.id = a.release_id
JOIN projects p ON p.id = r.project_id
ORDER BY p.name, r.version_num, pa.id;

-- name: ListOCIBlobFiles :many
-- Every project-scoped OCI blob link. These are not release-scoped, so release
-- eviction never frees them.
SELECT obl.id, obl.project_id, p.name AS project_name, obl.storage_key,
       obl.media_type, obl.size, obl.is_manifest, obl.created_at
FROM oci_blob_links obl
JOIN projects p ON p.id = obl.project_id
ORDER BY p.name, obl.id;

-- name: ListGoproxyBlobFiles :many
-- Every cached Go module zip. Cached upstream modules are not projects, so
-- release eviction never frees them either.
SELECT gv.id, gm.module_path, gv.version, gv.zip_storage_key, gv.zip_size, gv.fetched_at
FROM goproxy_versions gv
JOIN goproxy_modules gm ON gm.id = gv.module_id
WHERE gv.zip_storage_key != ''
ORDER BY gm.module_path, gv.version;

-- name: ListReleaseRetentionFacts :many
-- One row per release with the facts the keep-N and abandoned queries decide on:
-- how many newer published releases share its branch, whether an OCI tag or a
-- docker artifact pins it, and its published/draft state. The inventory turns
-- these into a per-file hold reason, so an operator can see WHY a release is
-- kept instead of only that it is.
SELECT r.id, r.project_id, p.name AS project_name, r.version, r.version_num,
       r.git_branch, r.published, r.draft, r.created_at,
       (SELECT COUNT(*) FROM releases r2
         WHERE r2.project_id = r.project_id
           AND r2.git_branch = r.git_branch
           AND r2.published = 1
           AND r2.version_num > r.version_num) AS newer_published_on_branch,
       (SELECT COUNT(*) FROM oci_tags t WHERE t.release_id = r.id) AS oci_tag_count,
       (SELECT COUNT(*) FROM artifacts a WHERE a.release_id = r.id AND a.kind = 'docker') AS docker_artifact_count
FROM releases r
JOIN projects p ON p.id = r.project_id
ORDER BY p.name, r.version_num;
