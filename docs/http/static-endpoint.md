# The unified download endpoint

`internal/static/`. Extracted verbatim from CLAUDE.md; paragraph breaks were added
at the existing topic boundaries, no wording changed.

Unified download endpoint on `static.{domain}/file`. All artifact downloads go
through here. Fmt interface with self-registration; query params: `project`, `v`,
`os`, `arch`, `fmt`. Includes raw/symbols formats and a bridge for
repackage-based formats.

## Signed temporary download tokens

Also accepts a `token` query param carrying a **signed temporary download token**
(`auth.MintDownloadToken`): the static route implements
`auth.PublicReadAuthorizer` so a token whose signature matches the exact
`(project, v, os, arch, fmt, debug)` tuple authorizes that one artifact under a
private project (the rest stays gated), mirroring
public-sites-under-private-projects. `token` is a known/canonical query param (so
it is **not** stripped by the canonicalization redirect like other unknown
params), and a token-bearing response is served `Cache-Control: private, no-store`
so a shared CDN never caches private content. `SignedURL` builds the canonical
static URL + token in one call.

## Encoding passthrough and canonicalization

The `raw` format serves the stored zstd blob **as-is** with `Content-Encoding:
zstd` (plus `Vary: Accept-Encoding`) when the client's `Accept-Encoding` lists
zstd and the artifact is not being stripped (`storage.CompressedGetter`),
offloading decompression to the client; otherwise it serves the decompressed bytes
as before. `canonicalQuery` also folds `os`/`arch` platform-name aliases to
canonical, so every spelling resolves to one cacheable URL via the
canonicalization redirect.

## Download attribution

Once a concrete artifact's bytes are served, `recordDownload` (`handler.go`)
appends a `download_events` row (migration 015: artifact_id, fmt, client IP --
first `X-Forwarded-For` hop -- User-Agent, and the authenticated `principal` --
session user or `token:<name>` -- for private pulls; `""` for anonymous public
pulls) and finally increments the previously-DEAD `download_counts`
(`IncrementDownloadCount` had no caller, so all counts read 0). Best-effort:
bookkeeping runs after the bytes are on the wire and its failures are logged,
never propagated. Origin-served only -- a public immutable artifact fetched
straight from a CDN edge never reaches the origin, so this is an audit trail
(every private/token pull + every cache miss), not a billing-grade counter. Read
back via admin `GET /api/projects/{name}/downloads`
(`internal/admin/downloads.go`, newest first, `?limit=` default 200 / max 1000;
also `db.ListDownloadEventsByRelease`).

## Caching contract for the whole path

All artifact downloads go through `static.{domain}/file?project=&v=&os=&arch=&fmt=`
-- a single CDN-cacheable endpoint with sorted query params, strong ETags, and
immutable cache headers. Format handlers (dl, apt, brew, npm) redirect to static
after resolving version/branch. `v=latest` returns 400 (callers must resolve
first). Repackage formats self-register via `Fmt` interface.

## Multi-platform artifacts

Artifact lookup resolves through `artifact_platforms`, so a platform covered by
a single multi-platform artifact (one APE) resolves to that artifact and serves
its blob. The ETag is derived from the artifact's storage key, so every covered
platform shares one ETag; `/dl` additionally folds covered platforms to the
artifact's canonical os/arch, so they share one URL too. Depth:
`docs/multi-platform-artifacts.md`.
