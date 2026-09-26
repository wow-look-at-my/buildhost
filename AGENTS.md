# Agent rules for buildhost

## Operator features go in the admin UI, never in the CLI

Nobody runs the `buildhost` CLI to operate the server. Operators use the admin dashboard (`internal/admin/`). A new operator capability ships as an admin API endpoint plus a control in the dashboard (`internal/admin/frontend/src/`). Backups, cleanup, inspection, repair and exports all follow this rule.

- Do not add a `cmd/buildhost/` subcommand for an operator task. Do not offer one as an alternative, and do not add one "as well".
- The CLI is for running the server (`serve`, `bootstrap`, `healthcheck`, `try-update`, `version`, `routes`) and for publishing from CI (`publish`, `publish-site`, `docker-push`).
- `gc`, `token` and `project` are older operator subcommands. They are not a precedent. Their dashboard pages are the supported path.
