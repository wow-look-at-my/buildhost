CREATE TABLE IF NOT EXISTS oidc_policies (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    issuer          TEXT NOT NULL,
    subject_pattern TEXT NOT NULL,
    project_id      INTEGER REFERENCES projects(id),
    scopes          TEXT NOT NULL DEFAULT 'read,write',
    created_at      DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(issuer, subject_pattern)
);

CREATE INDEX IF NOT EXISTS idx_oidc_policies_issuer ON oidc_policies(issuer);
