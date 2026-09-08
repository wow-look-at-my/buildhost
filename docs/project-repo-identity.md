# Projects follow their GitHub repo, by repo id

A project's name comes from its GitHub repo name. That name is mutable. A rename or a transfer changes it. The numeric repo id does not change. It survives both. It changes only when somebody deletes the repository and creates it again. Migration 014 pins that id on the project (`projects.github_repo_id`). `docs/security/oidc.md` covers the trust model it enforces.

The pin alone did not stop a rename from splitting a project in half. Resolution still went by NAME. The first publish after a rename found nothing under the new name. It then auto-provisioned a second project beside the first. The release history stayed under the old name. Every new release landed under the new one.

## A repo owns a namespace

A repo's projects are its root project, named after the repo, plus every `<root>/<child>` beneath it. A repo therefore has exactly one top-level project and any number of children.

`auth.reconcileRepoNamespace` rewrites the root segment of every project that carries the repo id when GitHub reports a new repo name. The root `A` becomes `B`. The child `A/child` becomes `B/child`. This runs on WRITE requests only. A read never mutates project state.

A child whose whole path repeats the root collapses onto the root. A repo named after its sole binary otherwise lands on `lpi/lpi`, which names the same thing at both levels.

A target name that is already taken is NOT resolved automatically. That name is a duplicate left by a rename older than this reconcile. To fold two release histories together has no safe default. The reconcile logs the `buildhost project merge` command that does it deliberately, and moves on.

## Old names keep resolving

A project name is a public contract. It appears in an apt package name, a brew formula, an npm package, and every `dl` URL a README ever printed. A rename that broke those links trades one problem for a worse one.

`project_aliases` (migration 018) records every name a project answered to before. `db.ResolveProject` looks up the name, then the alias table. Every backend resolves through it, because `requireProject` is the single resolution point. An old URL therefore keeps working after a rename or a merge.

A name is unique across `projects.name` and `project_aliases.name` together. SQLite cannot state that across separate tables. `db.NameAvailable` enforces it on both insert paths.

## Merging a duplicate

`buildhost project merge --from X --into Y` folds a stranded project into the survivor. `--dry-run` prints the plan and changes nothing.

Both projects number their releases from v1. The source's releases are therefore renumbered onto the end of the target's sequence. A semver project keeps its version strings, and only its ordering moves. A collision there is a conflict, not a rename.

The merge does this:

- It moves and renumbers the source's releases onto the target.
- It repoints sites, OCI tags, OCI blob links, API tokens, and OIDC policies.
- It drops an OCI blob link the target already holds. A shared storage key is the same bytes.
- It keeps the source's name, and the source's own aliases, as aliases of the target.
- It deletes the emptied source project row.

`download_counts` and `download_events` key off `artifact_id`. They follow their releases, and nothing here moves them.

Every change runs in one transaction. A drain check fails that transaction if any table still references the merged-away project. That check stops a table added later from leaving orphans behind in silence.

### Refusals

The merge stops rather than guess in these cases:

- The two projects do not share a pinned repo id. Nothing proves they are the same repository.
- Both deploy a site for the same branch, or publish the same OCI tag. To keep one silently drops the other.
- A semver version exists on both sides.
- The database changed between the plan and the apply.

### The snapshot

An apply writes a full `VACUUM INTO` snapshot beside the database first. It refuses to proceed when that snapshot fails. `VACUUM INTO` takes its own read transaction. The copy is therefore consistent. The server keeps running throughout. A merge rewrites history in place instead of appending to it. The snapshot is not optional.
