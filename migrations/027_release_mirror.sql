-- The release mirror holds GitHub release assets. The bytes are a row in
-- mirror_blobs. This table names the blob each asset URL gave.
--
-- Column types must stay the same as internal/db/schema.sql.
CREATE TABLE mirror_release_assets (
    upstream    TEXT NOT NULL,
    repository  TEXT NOT NULL,
    tag         TEXT NOT NULL,
    asset       TEXT NOT NULL,
    digest      TEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (upstream, repository, tag, asset)
);

CREATE INDEX IF NOT EXISTS idx_mirror_release_assets_digest ON mirror_release_assets(upstream, digest);
