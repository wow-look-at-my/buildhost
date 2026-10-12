-- Operator overrides of who may read what. Each level has three modes: 'auto'
-- (the source decides), 'public' or 'private'. 'auto' at the folder, release
-- and site levels is the absence of a row.
--
-- projects.is_private stays the EFFECTIVE value that every reader checks. The
-- code computes it from these tables and writes it back on each change.

-- A folder is a project-name prefix. It covers the project with that exact
-- name and every project under "<prefix>/".
CREATE TABLE visibility_folders (
    prefix     TEXT PRIMARY KEY,
    mode       TEXT NOT NULL CHECK (mode IN ('public', 'private')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

-- auto_private is what the source last reported: the GitHub repo visibility
-- for an OIDC publish, or the creator's choice for a project made by hand.
CREATE TABLE project_visibility (
    project_id   INTEGER PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    mode         TEXT NOT NULL DEFAULT 'auto' CHECK (mode IN ('auto', 'public', 'private')),
    auto_private INTEGER NOT NULL,
    updated_at   DATETIME NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO project_visibility (project_id, mode, auto_private)
SELECT id, 'auto', is_private FROM projects;

CREATE TABLE release_visibility (
    release_id INTEGER PRIMARY KEY REFERENCES releases(id) ON DELETE CASCADE,
    mode       TEXT NOT NULL CHECK (mode IN ('public', 'private')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

-- Keyed by branch, not by sites.id: the override outlives each redeploy.
CREATE TABLE site_visibility (
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    branch     TEXT NOT NULL,
    mode       TEXT NOT NULL CHECK (mode IN ('public', 'private')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (project_id, branch)
);
