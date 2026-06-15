-- Records the GitHub owner/repo a project was provisioned from (via the OIDC
-- publish subject, repo:OWNER/NAME:...). A browser "Sign in with GitHub" then
-- authorizes a private project by checking the signed-in user's access to this
-- exact repo, rather than an org allowlist. Empty for projects with no known
-- GitHub origin (those can't be opened by GitHub login -- only by a token).
ALTER TABLE projects ADD COLUMN github_repo TEXT NOT NULL DEFAULT '';
