-- Opt-in `service do` block in the project's generated Homebrew formula, so
-- `brew services start <tap>/<project>` manages the installed binary as a
-- login service (macOS user LaunchAgent; crash-only keep_alive). Project-level
-- and operator-set (PATCH /api/v1/projects/{name}) rather than a publish-time
-- field: the publishing repo's CI needs zero changes for its formula to gain
-- the block. Default 0 keeps every existing project's formula byte-identical.
ALTER TABLE projects ADD COLUMN brew_service INTEGER NOT NULL DEFAULT 0;
