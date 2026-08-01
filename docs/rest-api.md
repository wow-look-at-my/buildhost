# REST API surface

`internal/api/`. Extracted verbatim from CLAUDE.md; paragraph breaks were added at
the existing topic boundaries, no wording changed. The artifact-upload half of
this package (multi-platform fan-out, hash-reference uploads) lives in
`docs/uploads.md`.

REST API handlers (projects, releases, artifacts, publish, tokens). Each handler
file registers its own routes via auth.OnReady().

## Project settings

`PATCH /api/v1/projects/{project}` (`UpdateProjectSettings`) updates operator-set
project settings -- currently the one field `create_service` (pointer-typed
request fields, absent = unchanged); PATCH counts as a write verb in `parseRoute`,
so the centralized requireProject demands a write-scoped token authorized for the
project. The release-create body accepts the same optional `create_service` bool
(asserted on every publish, absent = untouched), making the publishing repo's CI
the declarative surface -- no manual API step in any documented flow. The setting
is packaging-format-AGNOSTIC ("this project runs as a background service"); each
format materializes it (brew: `service do` block; fmt=deb: auto-enabled systemd
user unit; raw/zip/npm/OCI: stored only).

## Release lookup

`GET /api/v1/projects/{project}/releases/{version}` resolves `latest` (and the
empty spec) to the apex latest published release via `db.GetLatestRelease` (newest
published on the project's default branch -- `projects.default_branch`, default
`master`, resolved per-project by buildhost itself from GitHub using the OIDC
token's repo identity, see `docs/apex-latest.md`; no release on that branch yet
means no apex latest, and there is deliberately no fallback to newest-overall that
would let a feature branch hijack `latest`) -- mirroring how dl/static/web already
treat `latest`, so a client (e.g. go-toolchain's background update check) gets the
newest release's metadata (`version`, `git_commit`, `published_at`) in one bounded
request instead of listing every release. Any other `{version}` is still an exact
match.

## Publish responses carry their artifacts

Both `PublishRelease` and `GetRelease` return `publishedRelease` (the `db.Release`
embedded, so every pre-existing top-level field is unchanged for older clients,
plus `artifacts`). Publish already loaded them for its no-artifacts check, so that
costs nothing. Pinned by `TestPublishRelease_ReturnsPublishedArtifacts` and
`TestGetRelease_ReturnsArtifacts`. The reason it exists is
`docs/artifact-storage-records.md`: the low-level publish composite needs the
artifact list to post storage records, and there is no artifacts-listing endpoint.

## Size caps

Upload size capped at 2 GiB (REST artifact PUT, configurable via
`BUILDHOST_MAX_UPLOAD_SIZE`); JSON endpoints capped at 1 MiB. OCI `docker push`
uploads each layer as a separate blob with its own cap
(`BUILDHOST_MAX_BLOB_SIZE`, default 10 GiB), so multi-GB images are not bound by
the REST cap.
