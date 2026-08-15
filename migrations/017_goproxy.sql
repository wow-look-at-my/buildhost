-- Go module proxy cache.
--
-- These tables are deliberately NOT projects/releases. A cached upstream module
-- is not a buildhost release: it must never appear in the browse frontend, the
-- package-manager surfaces, `latest` resolution or a download URL. It is cached
-- upstream content with its own lifecycle, so it gets its own tables and its own
-- blob references.
CREATE TABLE IF NOT EXISTS goproxy_modules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    module_path TEXT NOT NULL UNIQUE,
    -- 'github' (fetched direct from the API with buildhost's credential) or
    -- 'upstream' (passed through from the public module proxy).
    source TEXT NOT NULL DEFAULT 'github',
    -- The last fetch failure for this module, kept so a module that is failing is
    -- visible on the dashboard instead of only in a log line nobody reads. Empty
    -- kind means the last fetch succeeded.
    last_error_kind TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    last_error_at TEXT,
    last_success_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS goproxy_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    module_id INTEGER NOT NULL REFERENCES goproxy_modules(id) ON DELETE CASCADE,
    version TEXT NOT NULL,
    commit_sha TEXT NOT NULL DEFAULT '',
    committed_at TEXT,
    -- The module's go.mod verbatim (synthesized when the module has none).
    go_mod TEXT NOT NULL DEFAULT '',
    -- The canonical module zip in blob storage. Content-addressed like every
    -- other blob, and referenced from IsBlobReferenced so the GC does not sweep
    -- it out from under the cache.
    zip_storage_key TEXT NOT NULL DEFAULT '',
    zip_size INTEGER NOT NULL DEFAULT 0,
    fetched_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (module_id, version)
);

CREATE INDEX IF NOT EXISTS idx_goproxy_versions_module ON goproxy_versions (module_id);
CREATE INDEX IF NOT EXISTS idx_goproxy_versions_key ON goproxy_versions (zip_storage_key);
