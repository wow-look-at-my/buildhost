# The unified download endpoint

`internal/static/`. Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed.

Unified download endpoint on `static.{domain}/file`. All artifact downloads go through here. The Fmt interface self-registers. The query params are `project`, `v`, `os`, `arch` and `fmt`. It includes the raw and symbols formats plus a bridge for the repackage-based formats.

## Signed temporary download tokens

It also accepts a `token` query param that carries a **signed temporary download token** (`auth.MintDownloadToken`). The static route implements `auth.PublicReadAuthorizer`. A token whose signature matches the exact `(project, v, os, arch, fmt, debug)` tuple therefore authorizes that one artifact under a private project. The rest of the project stays gated. That mirrors a public site under a private project. `token` is a known, canonical query param, so the canonicalization redirect does **not** strip it the way it strips an unknown one.

A token-bearing response is served `Cache-Control: private, no-store`, so a shared CDN never caches private content. `SignedURL` builds the canonical static URL and the token in one call.

## Encoding passthrough and canonicalization

The `raw` format serves the stored zstd blob **as it is**, with `Content-Encoding: zstd` and `Vary: Accept-Encoding`. Conditions gate that. The client's `Accept-Encoding` must list zstd. The artifact must not be under a strip (`storage.CompressedGetter`). The client then does the decompression. Otherwise the format serves the decompressed bytes as before.

`canonicalQuery` also folds an `os` or `arch` platform-name alias to its canonical spelling. Every spelling therefore resolves to one cacheable URL through the canonicalization redirect.

## Transfer coding

The `raw` format negotiates the response coding from `Accept-Encoding` alone (`internal/static/encoding.go`). The order is zstd, then brotli, then gzip, then identity. An encoding the client did not name is never chosen, and a `q=0` entry is not an offer.

zstd comes first because the stored blob already is a zstd stream. A zstd request is served that stream through `storage.CompressedGetter`, untouched, with no stripping, no packing and no cache lookup. When the blob was stored uncompressed the variant is generated and cached like any other.

brotli and gzip wrap the payload the identity path serves, so every coding decodes to the same bytes. Every `raw` response carries `Vary: Accept-Encoding`, and the ETag covers the chosen coding: replaying an identity tag on a gzip response would otherwise serve the wrong bytes.

`debug=1` is the upload byte for byte. It is never transformed or recompressed.

## The recompression cache

Generated variants live on disk under the scratch root (`<TMPDIR>/buildhost-recompress`) in `internal/recompress`. An entry is keyed by the artifact's storage key, the payload transform version, the coding and the debug flag, so one artifact's bytes can never be served for another. Writes are atomic (produce a temporary file, then rename it to mode 0600), so a reader never sees a partial body, and a live entry is never rewritten: a repeated download reads the same bytes.

The cache is bounded by total bytes and by entry count, and it evicts the entry touched longest ago, which is not the entry created first. An entry expires after the TTL. A hit bypasses the throttle, because it performs no work.

## The recompression throttle

Starting a brotli or gzip variant costs CPU, and so does packing, so one client cannot start an unbounded number of them. `internal/recompress.Limiter` gives each client IP (the first `X-Forwarded-For` hop) an allowance per window. The allowance falls as the host's one-minute load average per CPU rises, and it is zero at or above the configured ceiling, where no new recompression starts.

A request that is not admitted is still a complete, correct download: it is answered with the identity path, which serves the transformed payload without a transfer coding. Throttling never turns a download into an error or a truncated body. The reverse proxy still owns general request rate limiting; this bounds work the origin performs for one client.

`canonicalQuery` also folds an `os` or `arch` platform-name alias to its canonical spelling. Every spelling therefore resolves to one cacheable URL through the canonicalization redirect.

## Download attribution

Once a concrete artifact's bytes are served, `recordDownload` in `handler.go` appends a `download_events` row. Migration multiple defines it: artifact_id, fmt, the client IP from the first `X-Forwarded-For` hop, the User-Agent, and the authenticated `principal`. That principal is the session user or `token:<name>` on a private pull. It is `""` on an anonymous public pull. `recordDownload` then increments the previously-DEAD `download_counts`. `IncrementDownloadCount` had no caller, so every count read 0.

The bookkeeping is best-effort. It runs after the bytes are on the wire, and its failures are logged and never propagated. It is origin-served only. A public immutable artifact fetched straight from a CDN edge never reaches the origin. This is therefore an audit trail over every private pull, every token pull and every cache miss. It is not a billing-grade counter.

Read it back through the admin `GET /api/projects/{name}/downloads` (`internal/admin/downloads.go`, newest first, `?limit=` default 200 and max 1000). `db.ListDownloadEventsByRelease` reads it too.

## Caching contract for the whole path

All artifact downloads go through `static.{domain}/file?project=&v=&os=&arch=&fmt=`. It is a single CDN-cacheable endpoint with sorted query params, strong ETags and immutable cache headers. A format handler (dl, apt, brew, npm) redirects to static after it resolves the version or branch. `v=latest` returns 400, because the caller must resolve it first. A repackage format self-registers through the `Fmt` interface.

## Multi-platform artifacts

Artifact lookup resolves through `artifact_platforms`. A platform covered by a single multi-platform artifact (one APE) therefore resolves to that artifact and serves its blob. The ETag comes from the artifact's storage key, so every covered platform shares one ETag. `/dl` also folds a covered platform to the artifact's canonical os and arch, so they share one URL too. Depth: `docs/multi-platform-artifacts.md`.
