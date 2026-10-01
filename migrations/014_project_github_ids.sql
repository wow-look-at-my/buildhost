-- Pins the numeric GitHub IDs behind projects.github_repo. GitHub owner/repo
-- NAMES are reusable: delete (or rename) a repo and anyone who claims the name
-- can mint OIDC tokens carrying the same "owner/repo" -- which is exactly why
-- GitHub's immutable OIDC subject claims (repos created after 2026-07-15)
-- carry numeric IDs (repo:OWNER@OWNERID/REPO@REPOID:...), and why the
-- repository_id / repository_owner_id claims exist. A project keyed on names
-- alone could therefore be taken over by a re-created ("resurrected") repo.
-- These columns pin the identity: recorded at provisioning (or on the first
-- ID-bearing publish for pre-existing projects), and every later OIDC request
-- whose token carries IDs must match them. Empty = not pinned (project has no
-- GitHub origin, or no ID-bearing token has published yet).
ALTER TABLE projects ADD COLUMN github_owner_id TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN github_repo_id TEXT NOT NULL DEFAULT '';
