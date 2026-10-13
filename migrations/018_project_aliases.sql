-- A project's name follows its GitHub repo. The repo id (projects.github_repo_id,
-- migration 014) survives a rename and a transfer, so buildhost can rewrite a
-- project's name when the repo's name changes instead of provisioning a second
-- project under the new name.
--
-- A rename must not break the consumers. A project name is a public contract: it
-- is in an apt package name, a brew formula, an npm package and every dl URL a
-- README ever printed. project_aliases holds every name a project answered to
-- before, so an old URL keeps resolving after the rename.
--
-- A name is unique across projects.name AND this table together. SQLite cannot
-- state that across two tables, so db.ReserveProjectName enforces it on both
-- insert paths.
CREATE TABLE project_aliases (
    name       TEXT PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id),
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_project_aliases_project ON project_aliases(project_id);

-- Reconciliation reads every project sharing one repo id, so that lookup is
-- indexed rather than a scan of the whole table on each write request.
CREATE INDEX IF NOT EXISTS idx_projects_github_repo_id ON projects(github_repo_id);
