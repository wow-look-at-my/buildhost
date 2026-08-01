# Tokens, scopes and temporary download links

Extracted verbatim from CLAUDE.md; paragraph breaks were added at the existing
topic boundaries, no wording changed.

## Tokens

Auth: Bearer token, Basic auth, or query param -- all resolve to the same token
system. Tokens are project-scoped or global; project-scoped tokens cannot escalate
privileges. Token expiry is enforced at lookup time.

Default token scope is "read" (least privilege). Scopes are `read`, `write`, and
`share` (`db.ValidScopes`); `share` is a distinct permission to mint temporary
download links (below), deliberately not implied by `write` so a CI/deploy token
cannot hand out shareable links. The bootstrap admin token holds
`read,write,share`.

**Token in query param**: Intentional for clients that cannot set headers (APT,
Brew). Mitigated by Referrer-Policy: no-referrer and redaction from OTEL trace
attributes.

## Temporary download links

A private artifact can be shared without a project token via a short-lived,
**artifact-bound, HMAC-signed** URL -- `static.{domain}/file?...&token=bhdl_...`.
Mint with `POST /api/v1/projects/{project}/download-links` (REST, requires a
`share`-scoped token authorized for the project) or `POST
/api/projects/{name}/download-links` (admin dashboard, trusted behind its reverse
proxy).

On a **private** project's release page the admin SPA's per-artifact
`raw`/`debug`/`fmt` links would 401 through `dl`, so they mint-on-click instead
(fetch a signed link, then download it); a plain "temp link" button copies a
shareable signed URL. Public projects keep the plain (cacheable, permanent) `dl`
links. The signature binds `(project, version, os, arch, fmt, debug)` + expiry
(default 1h, max 24h), so a leaked link exposes only that one file until it
expires; it is stateless (no DB row, not individually revocable -- rotate
`download-signing.key` to invalidate all). The link points at `static` directly (no
`dl` hop) with the version already resolved, since the binding needs an exact
version.

**Security review note**: `&token=bhdl_...` is a stateless HMAC-SHA256 signature
over `(project, version, os, arch, fmt, debug, expiry)` keyed by
`{DataDir}/download-signing.key` (32 random bytes, 0600, generated on first
start). It only ever *grants* read to the single artifact it is signed for under
an otherwise-private project -- it cannot escalate, cross projects, or outlive its
(capped, <=24h) expiry, and verification is constant-time (`hmac.Equal`). Minting
requires the `share` scope (REST) or the access-controlled admin dashboard.
Trade-off: links are not individually revocable before expiry (rotate the key to
invalidate all); acceptable for short-lived links. Token-gated responses are
`Cache-Control: private, no-store` so the shared CDN never caches them. Same
query-param exposure profile as the existing APT/Brew token param
(Referrer-Policy: no-referrer, OTEL redaction), but short-lived and
single-artifact rather than a full project token.

## Project auth

Private projects require auth on all endpoints including format-specific ones
(APT, Brew, NPM, OCI). Project auth is enforced once in the centralized
requireProject middleware -- handlers never check auth. Each backend defines a
RouteInfo implementation (private route struct) for full URL parsing.
