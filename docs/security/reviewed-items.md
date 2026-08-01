# Security notes (for future security reviews)

Extracted verbatim from CLAUDE.md, no wording changed. The larger items moved to
their own docs: `docs/security/github-signin.md` (browser sign-in, cross-domain
handoff, primary-domain scoping), `docs/security/oidc.md` (the whole OIDC trust
model), `docs/security/tokens-and-links.md` (scopes and temporary download links),
`docs/apex-latest.md` (the default-branch GitHub lookup).

The following items have been reviewed and addressed or are intentional design
choices:

- **Rate limiting**: Handled at the reverse proxy layer, not in the application
- **No TLS termination**: Intentional -- runs behind a reverse proxy in Docker
- **Strip temp file permissions**: Runs in a single-user Docker container;
  permissions are 0600 anyway
- **List endpoints**: No LIMIT -- all behind auth, SQLite serialized, not a DoS
  vector
- **Symlink rejection**: Storage layer rejects symlinks via Lstat check
- **Storage keys**: validated as hex SHA-256 to prevent path traversal
- **Admin dashboard auth**: None -- must be behind a reverse proxy with access
  control (Cloudflare Access, etc.)
- **Container user**: Runs as nonroot (UID 65532) via distroless base image
- **Graceful shutdown**: Server handles SIGTERM/SIGINT with 5-minute timeout for
  clean connection draining
- **Ready-to-update endpoint**: `GET /ready-to-update` on :8080 returns 200/503
  with no body content -- reveals only idle/busy state, no sensitive data
- **Inflight endpoint**: `GET /admin/inflight` on :9090 is unauthenticated -- same
  trust model as the rest of the admin dashboard (internal-only, behind reverse
  proxy)
- **No writes outside data dir**: Temp files use BUILDHOST_DATA_DIR/tmp, not
  system /tmp
- **Admin error messages**: Admin API handlers return generic error messages; raw
  errors are logged server-side only
- **Migrations**: Each migration runs in a single transaction (DDL +
  schema_migrations record) to prevent partial application on crash
- **Sites decompression**: Decompressed tar size is capped at 1 GiB to prevent
  gzip bomb attacks. ZIP uploads are also bounded by the 256 MiB upload limit and
  the 1 GiB decompressed tar cap.

## Sites security headers

Served site files drop the app's strict `Content-Security-Policy: default-src
'none'` and `X-Frame-Options: DENY` that the global middleware sets
(`internal/sites/serve.go` `setSiteSecurityHeaders`). Hosted sites are third-party
static content on the dedicated `sites.{domain}` subdomain (isolated from the
app/admin origins) and must be able to load their own and external assets, like
any static host. They set `Access-Control-Allow-Origin: *` for non-credentialed
cross-origin asset reads, plus `Cross-Origin-Opener-Policy: same-origin` and
`Cross-Origin-Embedder-Policy: credentialless`. `X-Content-Type-Options: nosniff`
is kept.

## Public sites under private projects

A site uploaded with `X-Public-Site: true` is served anonymously even when its
project is private. This is **opt-in per upload** (the publisher's own OIDC/token
write explicitly sets it) and **scoped to that one site branch** -- it does not
change the project's visibility, and release artifacts / other branches / the
`/branches` listing stay gated. The bypass is enforced centrally in
`requireProject` (the sites read route implements `PublicReadAuthorizer`), not by
the sites handler, so the "auth enforced once" invariant holds. The
`buildhost-publish-site` action defaults `public` to `false`; the PR-preview
reusable workflow opts in (`public: true`) because preview URLs are meant to be
shareable -- matching how a private repo can already serve a public GitHub Pages
site.
