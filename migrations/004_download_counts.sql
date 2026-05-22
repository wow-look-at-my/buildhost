CREATE TABLE IF NOT EXISTS download_counts (
    artifact_id INTEGER PRIMARY KEY REFERENCES artifacts(id),
    count       INTEGER NOT NULL DEFAULT 0
);
