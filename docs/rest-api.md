# REST API surface

`internal/api/`. Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed. The artifact-upload half of this package covers the multi-platform fan-out and the hash-reference uploads. It lives in `docs/uploads.md`.

REST API handlers (projects, releases, artifacts, publish, tokens). Each handler file registers its own routes in init() (see docs/http/routing-and-auth.md).

## Project settings

`PATCH /api/v1/projects/{project}` (`UpdateProjectSettings`) updates the operator-set project settings. There is one field today, `create_service`. Request fields are pointer-typed. An absent one is unchanged. `parseRoute` counts PATCH as a write verb. The centralized requireProject therefore demands a write-scoped token authorized for the project. The release-create body accepts the same optional `create_service` bool, asserted on every publish, and an absent one is untouched. The publishing repo's CI is therefore the declarative surface. No documented flow has a manual API step.

The setting is packaging-format-AGNOSTIC. It says "this project runs as a background service". Each format materializes it in its own way. brew emits a `service do` block. `fmt=deb` emits an auto-enabled systemd user unit. raw, zip, npm and OCI only store it.

## Release lookup

`GET /api/v1/projects/{project}/releases/{version}` resolves `latest`, and the empty spec, to the apex latest published release through `db.GetLatestRelease`. That is the newest published release on the project's default branch. The branch is `projects.default_branch`, which defaults to `master`. buildhost resolves it per project from GitHub with the OIDC token's repo identity. See `docs/apex-latest.md`. No release on that branch means no apex latest. There is deliberately no fallback to newest-overall, which lets a feature branch hijack `latest`. This mirrors how dl, static and the web frontend already treat `latest`. A client such as go-toolchain's background update check gets the newest release's `version`, `git_commit` and `published_at` in one bounded request. It does not list every release. Any other `{version}` is still an exact match.

## Publish responses carry their artifacts

Both `PublishRelease` and `GetRelease` return `publishedRelease` (the `db.Release` embedded, so every pre-existing top-level field is unchanged for older clients, plus `artifacts`). Publish already loaded them for its no-artifacts check, so that costs nothing. Pinned by `TestPublishRelease_ReturnsPublishedArtifacts` and `TestGetRelease_ReturnsArtifacts`. The reason it exists is `docs/artifact-storage-records.md`: the low-level publish composite needs the artifact list to post storage records, and there is no artifacts-listing endpoint.

## Size caps

The REST artifact PUT caps an upload at 2 GiB, configurable through `BUILDHOST_MAX_UPLOAD_SIZE`. A JSON endpoint caps at 1 MiB. OCI `docker push` uploads each layer as a separate blob with its own cap (`BUILDHOST_MAX_BLOB_SIZE`, default 10 GiB). A multi-GB image is therefore not bound by the REST cap.
