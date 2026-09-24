-- The default branch and every other branch get separate retention rules.
-- branch_keep_n: published releases kept on each non-default branch.
-- branch_ttl_days: how long a branch's releases outlive the deletion of the
-- branch on GitHub.
ALTER TABLE retention_settings ADD COLUMN branch_keep_n INTEGER NOT NULL DEFAULT 1;
ALTER TABLE retention_settings ADD COLUMN branch_ttl_days INTEGER NOT NULL DEFAULT 7;

-- When buildhost first learned that a branch no longer exists on GitHub. The
-- delete webhook writes the row, and so does the branch sync for a deletion the
-- webhook missed. The row goes away when the branch exists again.
CREATE TABLE IF NOT EXISTS deleted_branches (
    project_id INTEGER NOT NULL REFERENCES projects(id),
    branch     TEXT NOT NULL,
    deleted_at DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (project_id, branch)
);
