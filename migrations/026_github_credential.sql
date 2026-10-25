-- The GitHub token an operator sets in the admin dashboard. It authenticates
-- buildhost's own GitHub calls when BUILDHOST_GITHUB_TOKEN is not set.
CREATE TABLE github_credential (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    token      TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
