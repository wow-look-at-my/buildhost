-- A run lock records the first version a CI run resolved for a name, so every
-- later job of the run uses that version. repo_id, run_id and run_attempt come
-- from the verified OIDC token, never from the request. A row is written once
-- and never updated. The claim handler deletes the rows no run can still read.
CREATE TABLE run_locks (
    repo_id     TEXT NOT NULL,
    run_id      TEXT NOT NULL,
    run_attempt TEXT NOT NULL,
    name        TEXT NOT NULL,
    value       TEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (repo_id, run_id, run_attempt, name)
);

CREATE INDEX IF NOT EXISTS idx_run_locks_created ON run_locks(created_at);
