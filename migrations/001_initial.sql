CREATE TABLE IF NOT EXISTS projects (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    homepage    TEXT NOT NULL DEFAULT '',
    license     TEXT NOT NULL DEFAULT '',
    is_private  INTEGER NOT NULL DEFAULT 0,
    versioning  TEXT NOT NULL DEFAULT 'auto',
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS releases (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id   INTEGER NOT NULL REFERENCES projects(id),
    version      TEXT NOT NULL,
    version_num  INTEGER NOT NULL,
    git_branch   TEXT NOT NULL DEFAULT '',
    git_commit   TEXT NOT NULL DEFAULT '',
    notes        TEXT NOT NULL DEFAULT '',
    published    INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    published_at TEXT,
    UNIQUE(project_id, version)
);

CREATE TABLE IF NOT EXISTS artifacts (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    release_id           INTEGER NOT NULL REFERENCES releases(id),
    os                   TEXT NOT NULL,
    arch                 TEXT NOT NULL,
    kind                 TEXT NOT NULL DEFAULT 'binary',
    storage_key          TEXT NOT NULL,
    size                 INTEGER NOT NULL,
    sha256               TEXT NOT NULL,
    stripped_storage_key  TEXT NOT NULL DEFAULT '',
    stripped_size         INTEGER NOT NULL DEFAULT 0,
    stripped_sha256       TEXT NOT NULL DEFAULT '',
    debug_storage_key     TEXT NOT NULL DEFAULT '',
    debug_size            INTEGER NOT NULL DEFAULT 0,
    filename             TEXT NOT NULL DEFAULT '',
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(release_id, os, arch, kind)
);

CREATE TABLE IF NOT EXISTS packaged_artifacts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    artifact_id  INTEGER NOT NULL REFERENCES artifacts(id),
    format       TEXT NOT NULL,
    storage_key  TEXT NOT NULL,
    size         INTEGER NOT NULL,
    sha256       TEXT NOT NULL,
    filename     TEXT NOT NULL DEFAULT '',
    metadata     TEXT NOT NULL DEFAULT '{}',
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(artifact_id, format)
);

CREATE TABLE IF NOT EXISTS api_tokens (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    token_prefix TEXT NOT NULL,
    project_id   INTEGER REFERENCES projects(id),
    scopes       TEXT NOT NULL DEFAULT 'read,write',
    expires_at   TEXT,
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    last_used_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_releases_project ON releases(project_id, version_num DESC);
CREATE INDEX IF NOT EXISTS idx_releases_branch ON releases(project_id, git_branch, version_num DESC);
CREATE INDEX IF NOT EXISTS idx_artifacts_release ON artifacts(release_id);
CREATE INDEX IF NOT EXISTS idx_packaged_artifacts_artifact ON packaged_artifacts(artifact_id);
CREATE INDEX IF NOT EXISTS idx_tokens_hash ON api_tokens(token_hash);
