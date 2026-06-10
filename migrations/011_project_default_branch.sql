-- The repository's default branch. The apex "latest" download (no ?branch= and
-- no ?v=) resolves to the newest published release ON this branch, so a push to
-- a feature branch no longer hijacks "latest". Empty = unknown (legacy projects
-- and non-GHA publishers): resolution falls back to the newest release across
-- all branches, the historical behavior. It is set from the create-release
-- `default_branch` field, which the publish actions derive from the GitHub repo.
ALTER TABLE projects ADD COLUMN default_branch TEXT NOT NULL DEFAULT '';
