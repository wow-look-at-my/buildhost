# The dl redirect handler

`internal/dl/`. Extracted verbatim from CLAUDE.md, no wording changed.

Download handler on `dl.{domain}/{project}` with version/branch resolution via
query params. Redirects to static. Self-registering via auth.OnReady().

The `latest`/branch redirects (mutable pointers) are served `Cache-Control:
no-store` so a CDN never pins clients to a stale release after a new publish,
while an exact-version redirect (an immutable mapping) is `Cache-Control: public,
max-age=31536000, immutable`.

A **private** project's redirect (the route is ReadAccess-gated, so the caller
authenticated) is always a `302` with `Cache-Control: private, no-store` whose
Location carries a short-lived signed download token (`static.SignedURL`, 15 min):
clients drop the Authorization header when following the cross-host redirect to
static (curl semantics; Homebrew inherits them), so the Location must authorize
itself. Never cache it -- it embeds a live credential.

Platform-name aliases in `os`/`arch` (GitHub Actions' RUNNER_OS
`Linux`/`macOS`/`Windows`, RUNNER_ARCH `X64`/`ARM64`, uname's
`x86_64`/`aarch64`, ...) are folded to canonical via
`db.NormalizeOS`/`db.NormalizeArch` before redirecting, so callers can pass
platform names through verbatim.
