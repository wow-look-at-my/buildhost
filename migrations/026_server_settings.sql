-- A row holds the admin dashboard's value for one BUILDHOST_* variable. It
-- takes precedence over the environment. config.Settings lists the names.
--
-- Column types must stay the same as internal/db/schema.sql.
CREATE TABLE IF NOT EXISTS server_settings (
    name       TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
