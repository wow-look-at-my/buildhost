# Release mirror

`internal/mirror/release.go`. A read-only pull-through mirror of public GitHub release assets. It keeps GitHub's path, so a client changes only the host:

| GitHub | Mirror |
| --- | --- |
| `https://github.com/<owner>/<repo>/releases/download/<tag>/<asset>` | `https://mirror.{domain}/github.com/<owner>/<repo>/releases/download/<tag>/<asset>` |

`mirror.{domain}` is the mirror's own service subdomain, so no project name is reserved. The first path segment names the upstream host.

Every response carries `Cache-Control: private` and `Vary: Authorization`. The CDN caches a `.zip` or a `.tar.gz` by its extension, and a shared copy serves an anonymous caller without the token check.

## What it serves

`GET` and `HEAD` on `/github.com/<owner>/<repo>/releases/download/<tag>/<asset>`. A tag can hold a slash: the asset is the last segment. Every other method answers 405. `releases/latest/download/...` and source archives answer 404. A `latest` URL names a different asset over time, and the mirror stores an asset once.

## Auth

Every request needs a buildhost token with the `read` scope, sent as Bearer or as the Basic password. Without one the answer is 401 with a Basic challenge, and nothing reaches GitHub. The mirror fetches from GitHub anonymously, with the same TLS client as the registry mirror.

## Storage

The first request for an asset fetches it from GitHub, follows the redirect to GitHub's CDN. It streams the body to the client and to a temporary file. The file goes into the blob store under its SHA-256 when the whole body arrived. A body shorter than its `Content-Length` is not stored. A client that disconnects does not stop the fetch.

The bytes are a `mirror_blobs` row with upstream `github-releases`. `mirror_release_assets` names the digest each URL gave. GitHub ignores the case of owner and repo, so the row keys them in lower case. Tag and asset keep their case.

An asset is fixed at its first fetch. When the release owner replaces an asset, the mirror serves the first copy until the janitor evicts it. Pin the SHA-256 in the client.

## Errors

An asset GitHub does not have is a 404. A 429 from GitHub is a 429. Every other GitHub failure is a 502 that names GitHub's answer. A stored asset never asks GitHub, so it serves through a GitHub outage.

## Eviction

The registry mirror's janitor covers release assets. A blob nothing read for `BUILDHOST_MIRROR_KEEP` goes, with the asset rows that name it. See `registry-mirror.md`.

## Dashboard

The Registry Mirror page (`#/mirror`, `GET /api/mirror`) lists every release asset in its own table. The stored blobs show under upstream `github-releases`.
