# Tokens, scopes and temporary download links

Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed.

## Tokens

Auth arrives as a Bearer token, as Basic auth, or as a query param. All three resolve to the same token system. A token is project-scoped or global. A project-scoped token cannot escalate privileges. Token expiry is enforced at lookup time.

The default token scope is "read", which is least privilege. The scopes are `read`, `write` and `share` (`db.ValidScopes`). `share` is a distinct permission to mint the temporary download links below. `write` deliberately does not imply it, so a CI or deploy token cannot hand out a shareable link. The bootstrap admin token holds `read,write,share`.

**Token in query param**: Intentional for clients that cannot set headers (APT, Brew). Mitigated by Referrer-Policy: no-referrer and redaction from OTEL trace attributes.

## Temporary download links

A private artifact can be shared without a project token via a short-lived, **artifact-bound, HMAC-signed** URL -- `static.{domain}/file?...&token=bhdl_...`. Mint with `POST /api/v1/projects/{project}/download-links` (REST, requires a `share`-scoped token authorized for the project) or `POST /api/projects/{name}/download-links` (admin dashboard, trusted behind its reverse proxy).

On a **private** project's release page the admin SPA's per-artifact `raw`, `debug` and `fmt` links 401 through `dl`. They mint on click instead: fetch a signed link, then download it. A plain "temp link" button copies a shareable signed URL. A public project keeps the plain `dl` links, which are cacheable and permanent. The signature binds `(project, version, os, arch, fmt, debug)` and the expiry, which defaults to 1h and caps at 24h. A leaked link therefore exposes only that one file until it expires. It is stateless. There is no DB row. It is not individually revocable. Rotate `download-signing.key` to invalidate every one of them. The link points at `static` directly with no `dl` hop, and the version is already resolved, because the binding needs an exact version.

**Security review note**: `&token=bhdl_...` is a stateless HMAC-SHA256 signature over `(project, version, os, arch, fmt, debug, expiry)`. Its key is `{DataDir}/download-signing.key`, 32 random bytes at mode 0600, generated on first start. It only ever *grants* read to the single artifact it is signed for, under an otherwise-private project. It cannot escalate. It cannot cross projects. It cannot outlive its capped expiry of 24h or less. Verification is constant-time (`hmac.Equal`). Minting needs the `share` scope over REST, or the access-controlled admin dashboard. There is a trade-off. A link is not individually revocable before it expires, and a key rotation invalidates every one of them. That is acceptable for a short-lived link. A token-gated response is `Cache-Control: private, no-store`, so the shared CDN never caches it. The query-param exposure profile matches the existing APT and Brew token param, with Referrer-Policy: no-referrer and OTEL redaction. This one is short-lived and single-artifact instead of a full project token.

## Project auth

Private projects require auth on all endpoints including format-specific ones (APT, Brew, NPM, OCI). Project auth is enforced once in the centralized requireProject middleware -- handlers never check auth. Each backend defines a RouteInfo implementation (private route struct) for full URL parsing.
