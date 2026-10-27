# Run files

A run file is a file one job of a GitHub Actions run hands to later jobs of the same run. The `buildhost-handoff` action is the client: `mode: send` packs a directory as a zstd-compressed tar and stores it under a name, and `mode: receive` restores it. `buildhost archive` (`internal/runarchive`) packs and extracts, so every host writes the same pax archive with nanosecond mtimes, symlinks and hardlinks. The action runs the CLI that `BUILDHOST_CLI` names, else the newest published one. It replaces a hand-off through the Actions cache. It also replaces which a busy repository evicts between the job that saves an entry and the job that reads it.

A run file is not a release, a project or an artifact. Nothing outside its run reads it, `dl` and `static` never serve it, and the release retention never sees it.

## Contract

- A file belongs to one workflow run of one repository. The key is the repository id and `run_id` from the caller's verified OIDC token, plus a name. `run_attempt` is not part of the key, so a re-run of a failed job reads what the first try sent.
- A put of a name the run holds replaces it. The blob it held is deleted when nothing else references it.
- A get of a name the run does not hold answers 404, and the message names every file the run does hold.
- A get honors `Range`, and carries the blob's sha256 as `X-Content-SHA256`. The action resumes a broken download from the bytes it has, then checks the whole file against that sum. A transport failure is tried again every few seconds until the job timeout. A server outage costs only its length. A 401, 403 or 404, or a whole download whose sum differs, is last.
- The body is stored as sent, without the storage layer's own compression, because the action compresses it already. That is also what makes it seekable for `Range`.

## Authentication

The same as a run lock (`docs/run-locks.md`): the job's OIDC token, minted with the server URL as its audience, and `permissions: id-token: write`. The request names its run as `repository` and `run_id`. A request that names another run than its token is refused with 403. A body over the direct upload limit goes through an upload session, which needs a token the event allowlist accepts.

## Endpoints

- `PUT /api/v1/run-files/{name}?repository=&run_id=` stores the body, or a finished upload session through `upload_session`. It answers 201 with `{"name", "size", "sha256"}`.
- `GET /api/v1/run-files/{name}?repository=&run_id=` serves the file.
- `GET /api/v1/run-files?repository=&run_id=` lists the run's files.

A name is 1 to 128 of `[A-Za-z0-9._-]`, starting with a letter or digit.

## Deletion

`internal/runfiles` runs a janitor every few minutes, on every server. It is not opt-in.

- It asks GitHub for the status of each live run that holds files (`GET /repos/{repo}/actions/runs/{run_id}`, with the App installation token or `BUILDHOST_GITHUB_TOKEN`). A completed run gets its `ended_at` from the run's `updated_at`.
- It deletes a run's files `BUILDHOST_RUN_FILES_KEEP` after the run ended. The default is `24h`.
- It deletes ended runs sooner, oldest-ended first, and stops once free disk is back above it. This happens while free disk on the data directory is below `BUILDHOST_RUN_FILES_MIN_FREE_PERCENT` (default `10`). `0` turns this off.
- It never deletes a live run early, because a job of that run still waits for its files. When only live runs are left and disk is still low, it logs an error with the bytes the live runs hold.
- A status GitHub does not answer is not an end while the run still uses its files. The janitor logs a warning and asks again on the next pass. A run whose status GitHub does not answer and that nothing used for `BUILDHOST_RUN_FILES_KEEP` goes. Without this, a server with no GitHub credential (`github_credential: none` on `/healthz`) keeps every run's files for `MaxRunLifetime`, many days. A run whose end GitHub never reports goes after `MaxRunLifetime`, the longest a run can last.
- A send or a receive marks the run live again. A re-run of a completed run therefore keeps its files until GitHub reports the re-run complete.
- It waits while writes are in flight, as the release retention sweep does, so it cannot delete a blob an upload stored again.

The admin dashboard's **Run Files** page reads `GET /api/run-files` on the admin port. It shows:

- the disk of the data directory, split into run files, everything else and free, with the free floor marked
- a lifecycle timeline with one lane per run: solid while live, hatched for the keep window after it ended, a pip per file sent
- a runs table with live and ended filters. A row expands to its files with size, reads, last read and sha256
- bytes per run, and a line on the janitor's last sweep: what it asked GitHub, what it deleted, any status check that failed

A full `GET` of a run file counts as a read. A ranged `GET` does not, since one download can make many of them.

A malformed `BUILDHOST_RUN_FILES_KEEP` or `BUILDHOST_RUN_FILES_MIN_FREE_PERCENT` fails startup.
