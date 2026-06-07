-- UI-editable retention policy: how many published releases to keep per
-- (project, git branch) and the recency guard in hours (releases newer than this
-- are never evicted). A single row (id = 1). Seeded from the BUILDHOST_RETENTION_*
-- defaults on first start and managed from the admin dashboard thereafter. The
-- background sweeper's cadence (BUILDHOST_RETENTION_INTERVAL) and whether it
-- auto-enforces (BUILDHOST_RETENTION_ENFORCE) stay env-only -- deploy-level safety
-- that the dashboard cannot turn on.
CREATE TABLE IF NOT EXISTS retention_settings (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    keep_n        INTEGER NOT NULL DEFAULT 10,
    recency_hours INTEGER NOT NULL DEFAULT 24,
    updated_at    DATETIME NOT NULL DEFAULT (datetime('now'))
);
