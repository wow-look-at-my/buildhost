CREATE TABLE projects (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    homepage    TEXT NOT NULL DEFAULT '',
    license     TEXT NOT NULL DEFAULT '',
    is_private  INTEGER NOT NULL DEFAULT 0,
    versioning  TEXT NOT NULL DEFAULT 'auto',
    github_repo TEXT NOT NULL DEFAULT '',
    github_owner_id TEXT NOT NULL DEFAULT '',
    github_repo_id TEXT NOT NULL DEFAULT '',
    default_branch TEXT NOT NULL DEFAULT 'master',
    create_service INTEGER NOT NULL DEFAULT 0,
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
    draft        INTEGER NOT NULL DEFAULT 0,
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
    exe_format            TEXT NOT NULL DEFAULT '',
    created_at            DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(release_id, os, arch, kind)
);

CREATE TABLE artifact_platforms (
    artifact_id INTEGER NOT NULL REFERENCES artifacts(id),
    release_id  INTEGER NOT NULL REFERENCES releases(id),
    kind        TEXT NOT NULL,
    os          TEXT NOT NULL,
    arch        TEXT NOT NULL,
    ordinal     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (artifact_id, os, arch)
);
CREATE UNIQUE INDEX idx_artifact_platforms_slot ON artifact_platforms(release_id, kind, os, arch);

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

CREATE TABLE download_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    artifact_id INTEGER NOT NULL REFERENCES artifacts(id),
    fmt         TEXT NOT NULL DEFAULT 'raw',
    client_ip   TEXT NOT NULL DEFAULT '',
    user_agent  TEXT NOT NULL DEFAULT '',
    principal   TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_download_events_artifact ON download_events(artifact_id);
CREATE INDEX IF NOT EXISTS idx_download_events_created  ON download_events(created_at DESC);

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

-- Go module proxy cache (mirrored from migrations/017_goproxy.sql). Cached
-- upstream modules are NOT projects/releases -- see that migration for why.
CREATE TABLE goproxy_modules (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    module_path     TEXT NOT NULL UNIQUE,
    source          TEXT NOT NULL DEFAULT 'github',
    last_error_kind TEXT NOT NULL DEFAULT '',
    last_error      TEXT NOT NULL DEFAULT '',
    last_error_at   DATETIME,
    last_success_at DATETIME,
    created_at      DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE goproxy_versions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    module_id       INTEGER NOT NULL REFERENCES goproxy_modules(id) ON DELETE CASCADE,
    version         TEXT NOT NULL,
    commit_sha      TEXT NOT NULL DEFAULT '',
    committed_at    DATETIME,
    go_mod          TEXT NOT NULL DEFAULT '',
    zip_storage_key TEXT NOT NULL DEFAULT '',
    zip_size        INTEGER NOT NULL DEFAULT 0,
    fetched_at      DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(module_id, version)
);

CREATE INDEX IF NOT EXISTS idx_goproxy_versions_module ON goproxy_versions(module_id);
CREATE INDEX IF NOT EXISTS idx_goproxy_versions_key    ON goproxy_versions(zip_storage_key);
