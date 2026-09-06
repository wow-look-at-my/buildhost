# The unified download endpoint

`internal/static/`. Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed.

Unified download endpoint on `static.{domain}/file`. All artifact downloads go through here. The Fmt interface self-registers. The query params are `project`, `v`, `os`, `arch` and `fmt`. It includes the raw and symbols formats plus a bridge for the repackage-based formats.

## Signed temporary download tokens

It also accepts a `token` query param that carries a **signed temporary download token** (`auth.MintDownloadToken`). The static route implements `auth.PublicReadAuthorizer`. A token whose signature matches the exact `(project, v, os, arch, fmt, debug)` tuple therefore authorizes that one artifact under a private project. The rest of the project stays gated. That mirrors a public site under a private project. `token` is a known, canonical query param, so the canonicalization redirect does **not** strip it the way it strips an unknown one. A token-bearing response is served `Cache-Control: private, no-store`, so a shared CDN never caches private content. `SignedURL` builds the canonical static URL and the token in one call.

## Encoding passthrough and canonicalization

The `raw` format serves the stored zstd blob **as it is**, with `Content-Encoding: zstd` and `Vary: Accept-Encoding`. Two conditions gate that. The client's `Accept-Encoding` must list zstd. The artifact must not be under a strip (`storage.CompressedGetter`). The client then does the decompression. Otherwise the format serves the decompressed bytes as before. `canonicalQuery` also folds an `os` or `arch` platform-name alias to its canonical spelling. Every spelling therefore resolves to one cacheable URL through the canonicalization redirect.

## Download attribution

Once a concrete artifact's bytes are served, `recordDownload` in `handler.go` appends a `download_events` row. Migration 015 defines it: artifact_id, fmt, the client IP from the first `X-Forwarded-For` hop, the User-Agent, and the authenticated `principal`. That principal is the session user or `token:<name>` on a private pull. It is `""` on an anonymous public pull. `recordDownload` then increments the previously-DEAD `download_counts`. `IncrementDownloadCount` had no caller, so every count read 0.

The bookkeeping is best-effort. It runs after the bytes are on the wire, and its failures are logged and never propagated. It is origin-served only. A public immutable artifact fetched straight from a CDN edge never reaches the origin. This is therefore an audit trail over every private pull, every token pull and every cache miss. It is not a billing-grade counter. Read it back through the admin `GET /api/projects/{name}/downloads` (`internal/admin/downloads.go`, newest first, `?limit=` default 200 and max 1000). `db.ListDownloadEventsByRelease` reads it too.

## Caching contract for the whole path

All artifact downloads go through `static.{domain}/file?project=&v=&os=&arch=&fmt=`. It is a single CDN-cacheable endpoint with sorted query params, strong ETags and immutable cache headers. A format handler (dl, apt, brew, npm) redirects to static after it resolves the version or branch. `v=latest` returns 400, because the caller must resolve it first. A repackage format self-registers through the `Fmt` interface.

## Multi-platform artifacts

Artifact lookup resolves through `artifact_platforms`. A platform covered by a single multi-platform artifact (one APE) therefore resolves to that artifact and serves its blob. The ETag comes from the artifact's storage key, so every covered platform shares one ETag. `/dl` also folds a covered platform to the artifact's canonical os and arch, so they share one URL too. Depth: `docs/multi-platform-artifacts.md`.
