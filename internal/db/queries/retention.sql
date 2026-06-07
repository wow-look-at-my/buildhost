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
-- evictable when keep_n or more NEWER published releases exist on the same branch
-- (i.e. its newest-first rank is > keep_n). Excludes anything newer than the
-- recency cutoff, tagged releases, and pushed-docker releases (their blobs live
-- in project-scoped oci_blob_links, not release-cascade-able). The per-branch tip
-- has zero newer releases, so it is inherently kept for any keep_n >= 0.
-- Correlated-subquery form (sqlc's SQLite analyzer does not support window-fn
-- aliases in WHERE).
SELECT r.id, r.project_id, r.git_branch, r.version, r.version_num
FROM releases r
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
  ) >= sqlc.arg(keep_n)
ORDER BY r.project_id, r.git_branch, r.version_num DESC;

-- name: ListAbandonedReleases :many
-- Unpublished (partial/failed upload) releases older than the cutoff.
SELECT r.id, r.project_id, r.git_branch, r.version
FROM releases r
WHERE r.published = 0 AND r.created_at < datetime(sqlc.arg(cutoff));

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
      ) >= sqlc.arg(keep_n)
)
SELECT CAST(
    COALESCE((SELECT SUM(a.size + a.stripped_size + a.debug_size)
              FROM artifacts a WHERE a.release_id IN (SELECT id FROM evictable)), 0)
  + COALESCE((SELECT SUM(pa.size) FROM packaged_artifacts pa
              JOIN artifacts a ON pa.artifact_id = a.id
              WHERE a.release_id IN (SELECT id FROM evictable)), 0)
AS INTEGER) AS reclaimable_bytes;
