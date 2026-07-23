-- Per-download attribution. Until now buildhost recorded only an aggregate
-- download_counts tally -- and never even incremented it (IncrementDownloadCount
-- had no caller), so "who downloaded what, and when" was unanswerable. Each row
-- here records one served artifact download: the artifact (whose FK pins project
-- + version + os/arch), the delivered format, and the requester as far as it is
-- known -- client IP and User-Agent for anonymous public pulls, plus the
-- authenticated principal (session user, or "token:<name>") for private pulls.
-- Public immutable artifacts served from a CDN edge never reach the origin, so
-- this captures origin-served downloads (every private/token pull, every cache
-- miss): an audit trail, not a billing-grade counter.
CREATE TABLE IF NOT EXISTS download_events (
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
