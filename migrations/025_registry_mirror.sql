-- The registry mirror holds content it pulled from a public registry. A row
-- here is not a release and not a project, so it never shows on a package
-- surface. used_at moves on each read, and the mirror janitor evicts by it.
--
-- Column types must stay the same as internal/db/schema.sql.
CREATE TABLE mirror_blobs (
    upstream    TEXT NOT NULL,
    digest      TEXT NOT NULL,
    -- The manifest media type. Empty for a layer or a config blob.
    media_type  TEXT NOT NULL DEFAULT '',
    storage_key TEXT NOT NULL,
    size        INTEGER NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    used_at     DATETIME NOT NULL DEFAULT (datetime('now')),
    reads       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (upstream, digest)
);

CREATE INDEX IF NOT EXISTS idx_mirror_blobs_storage_key ON mirror_blobs(storage_key);
CREATE INDEX IF NOT EXISTS idx_mirror_blobs_used_at ON mirror_blobs(used_at);

-- The digest a tag named the last time the upstream answered for it.
CREATE TABLE mirror_tags (
    upstream    TEXT NOT NULL,
    repository  TEXT NOT NULL,
    tag         TEXT NOT NULL,
    digest      TEXT NOT NULL,
    media_type  TEXT NOT NULL,
    checked_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (upstream, repository, tag)
);
