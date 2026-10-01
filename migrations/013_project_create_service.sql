-- Packaging-agnostic opt-in: the project's installed binary runs as a
-- background service, materialized per download format (Homebrew formulas
-- gain a `service do` block for `brew services`; on-the-fly debs ship a
-- systemd user unit; other formats store the flag without materializing).
-- Declared by the publishing repo's CI on release-create (asserted every
-- publish; absent = untouched) or set by an operator via
-- PATCH /api/v1/projects/{name}. Default 0 keeps every existing artifact
-- byte-identical.
ALTER TABLE projects ADD COLUMN create_service INTEGER NOT NULL DEFAULT 0;
