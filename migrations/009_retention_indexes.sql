-- Indexes supporting retention/GC. No new columns or tables: keep-N, the
-- abandoned-upload sweep, and the refcount sweep all read existing columns.
--
-- The five storage_key indexes make the global IsBlobReferenced probe fast --
-- it checks each freed candidate key against five columns once per swept blob.
-- The artifacts/packaged_artifacts UNIQUE constraints index (release_id, ...) and
-- (artifact_id, ...) but NOT storage_key; oci_blob_links UNIQUE is
-- (project_id, storage_key), so a probe by storage_key alone is unindexed there
-- too. The releases index drives the keep-N window partition + per-branch tip.
CREATE INDEX IF NOT EXISTS idx_releases_project_branch_version ON releases(project_id, git_branch, version_num DESC);
CREATE INDEX IF NOT EXISTS idx_artifacts_storage_key  ON artifacts(storage_key);
CREATE INDEX IF NOT EXISTS idx_artifacts_stripped_key ON artifacts(stripped_storage_key);
CREATE INDEX IF NOT EXISTS idx_artifacts_debug_key    ON artifacts(debug_storage_key);
CREATE INDEX IF NOT EXISTS idx_packaged_storage_key   ON packaged_artifacts(storage_key);
CREATE INDEX IF NOT EXISTS idx_oci_blob_links_skey    ON oci_blob_links(storage_key);
