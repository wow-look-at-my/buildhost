-- Run files are the files one job of a CI run hands to later jobs of the same
-- run. repo_id and run_id come from the verified OIDC token, never from the
-- request. run_attempt is not part of the key, so a re-run reads what an
-- earlier attempt of the run sent. ended_at is the time GitHub reports the run
-- completed. The run files janitor deletes a run some time after that.
CREATE TABLE run_storage_runs (
    repo_id    TEXT NOT NULL,
    run_id     TEXT NOT NULL,
    repo_path  TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    used_at    DATETIME NOT NULL DEFAULT (datetime('now')),
    checked_at DATETIME,
    ended_at   DATETIME,
    PRIMARY KEY (repo_id, run_id)
);

CREATE TABLE run_files (
    repo_id     TEXT NOT NULL,
    run_id      TEXT NOT NULL,
    name        TEXT NOT NULL,
    storage_key TEXT NOT NULL,
    size        INTEGER NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (repo_id, run_id, name)
);

CREATE INDEX IF NOT EXISTS idx_run_files_storage_key ON run_files(storage_key);
