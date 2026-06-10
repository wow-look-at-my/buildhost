CREATE TABLE projects (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    homepage    TEXT NOT NULL DEFAULT '',
    license     TEXT NOT NULL DEFAULT '',
    is_private  INTEGER NOT NULL DEFAULT 0,
    versioning  TEXT NOT NULL DEFAULT 'auto',
    default_branch TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at  DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE releases (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id   INTEGER NOT NULL REFERENCES projects(id),
    version      TEXT NOT NULL,
    version_num  INTEGER NOT NULL,
    git_branch   TEXT NOT NULL DEFAULT '',
    git_commit   TEXT NOT NULL DEFAULT '',
    notes        TEXT NOT NULL DEFAULT '',
    oci_user     TEXT NOT NULL DEFAULT '',
    published    INTEGER NOT NULL DEFAULT 0,
    created_at   DATETIME NOT NULL DEFAULT (datetime('now')),
    published_at DATETIME,
    UNIQUE(project_id, version)
);

CREATE TABLE artifacts (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    release_id            INTEGER NOT NULL REFERENCES releases(id),
    os                    TEXT NOT NULL,
    arch                  TEXT NOT NULL,
    kind                  TEXT NOT NULL DEFAULT 'binary',
    storage_key           TEXT NOT NULL,
    size                  INTEGER NOT NULL,
    sha256                TEXT NOT NULL,
    stripped_storage_key  TEXT NOT NULL DEFAULT '',
    stripped_size         INTEGER NOT NULL DEFAULT 0,
    stripped_sha256       TEXT NOT NULL DEFAULT '',
    debug_storage_key     TEXT NOT NULL DEFAULT '',
    debug_size            INTEGER NOT NULL DEFAULT 0,
    filename              TEXT NOT NULL DEFAULT '',
    created_at            DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(release_id, os, arch, kind)
);

CREATE TABLE packaged_artifacts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    artifact_id  INTEGER NOT NULL REFERENCES artifacts(id),
    format       TEXT NOT NULL,
    storage_key  TEXT NOT NULL,
    size         INTEGER NOT NULL,
    sha256       TEXT NOT NULL,
    filename     TEXT NOT NULL DEFAULT '',
    metadata     TEXT NOT NULL DEFAULT '{}',
    created_at   DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(artifact_id, format)
);

CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    token_prefix TEXT NOT NULL,
    project_id   INTEGER REFERENCES projects(id),
    scopes       TEXT NOT NULL DEFAULT 'read,write',
    expires_at   DATETIME,
    created_at   DATETIME NOT NULL DEFAULT (datetime('now')),
    last_used_at DATETIME
);

CREATE TABLE oidc_policies (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    issuer          TEXT NOT NULL,
    subject_pattern TEXT NOT NULL,
    audience        TEXT NOT NULL DEFAULT '',
    project_id      INTEGER REFERENCES projects(id),
    scopes          TEXT NOT NULL DEFAULT 'read,write',
    created_at      DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(issuer, subject_pattern)
);

CREATE TABLE download_counts (
    artifact_id INTEGER PRIMARY KEY REFERENCES artifacts(id),
    count       INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE sites (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES projects(id),
    branch      TEXT NOT NULL,
    storage_key TEXT NOT NULL,
    size        INTEGER NOT NULL,
    sha256      TEXT NOT NULL,
    file_count  INTEGER NOT NULL DEFAULT 0,
    git_commit  TEXT NOT NULL DEFAULT '',
    is_public   INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(project_id, branch)
);

CREATE TABLE oci_blob_links (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES projects(id),
    storage_key TEXT NOT NULL,
    media_type  TEXT NOT NULL DEFAULT '',
    size        INTEGER NOT NULL DEFAULT 0,
    is_manifest INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(project_id, storage_key)
);

CREATE TABLE oci_tags (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id      INTEGER NOT NULL REFERENCES projects(id),
    tag             TEXT NOT NULL,
    manifest_digest TEXT NOT NULL,
    release_id      INTEGER NOT NULL REFERENCES releases(id),
    updated_at      DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(project_id, tag)
);

CREATE TABLE retention_settings (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    keep_n        INTEGER NOT NULL DEFAULT 10,
    recency_hours INTEGER NOT NULL DEFAULT 24,
    updated_at    DATETIME NOT NULL DEFAULT (datetime('now'))
);

-- Retention/GC indexes (mirrored from migrations/009_retention_indexes.sql).
CREATE INDEX IF NOT EXISTS idx_releases_project_branch_version ON releases(project_id, git_branch, version_num DESC);
CREATE INDEX IF NOT EXISTS idx_artifacts_storage_key  ON artifacts(storage_key);
CREATE INDEX IF NOT EXISTS idx_artifacts_stripped_key ON artifacts(stripped_storage_key);
CREATE INDEX IF NOT EXISTS idx_artifacts_debug_key    ON artifacts(debug_storage_key);
CREATE INDEX IF NOT EXISTS idx_packaged_storage_key   ON packaged_artifacts(storage_key);
CREATE INDEX IF NOT EXISTS idx_oci_blob_links_skey    ON oci_blob_links(storage_key);
