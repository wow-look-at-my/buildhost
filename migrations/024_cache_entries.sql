-- A cache entry is a directory a CI job saves under a key, for jobs of later
-- runs to restore. repo_id and ref come from the verified OIDC token, never
-- from the request. ref scopes the entry: a run reads the entries of its own
-- ref and of the default branch. A key in one scope is written once. used_at
-- moves on each restore, and the cache janitor evicts by it.
CREATE TABLE cache_entries (
    repo_id     TEXT NOT NULL,
    ref         TEXT NOT NULL,
    key         TEXT NOT NULL,
    repo_path   TEXT NOT NULL,
    storage_key TEXT NOT NULL,
    size        INTEGER NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    used_at     DATETIME NOT NULL DEFAULT (datetime('now')),
    reads       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (repo_id, ref, key)
);

CREATE INDEX IF NOT EXISTS idx_cache_entries_storage_key ON cache_entries(storage_key);
CREATE INDEX IF NOT EXISTS idx_cache_entries_used_at ON cache_entries(used_at);
