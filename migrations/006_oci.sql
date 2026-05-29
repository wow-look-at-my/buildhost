-- oci_blob_links associates a pushed OCI blob or manifest (content-addressed by
-- its digest, which is also its storage key) with the project it was pushed to.
-- The OCI download path (serveBlob / serveManifestByDigest) gates on
-- BlobBelongsToProject, so every pushed blob must be linked here to be servable.
CREATE TABLE IF NOT EXISTS oci_blob_links (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES projects(id),
    storage_key TEXT NOT NULL,
    media_type  TEXT NOT NULL DEFAULT '',
    size        INTEGER NOT NULL DEFAULT 0,
    is_manifest INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(project_id, storage_key)
);

CREATE INDEX IF NOT EXISTS idx_oci_blob_links_project_key
    ON oci_blob_links(project_id, storage_key);

-- oci_tags maps a pushed image tag (a git sha, "latest", a semver, ...) to the
-- manifest digest and release it currently points at. This decouples the mutable
-- docker tag from buildhost's immutable integer release version, and makes
-- "latest" a real alias rather than a literal release.
CREATE TABLE IF NOT EXISTS oci_tags (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id      INTEGER NOT NULL REFERENCES projects(id),
    tag             TEXT NOT NULL,
    manifest_digest TEXT NOT NULL,
    release_id      INTEGER NOT NULL REFERENCES releases(id),
    updated_at      DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(project_id, tag)
);
