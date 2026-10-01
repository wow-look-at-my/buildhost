# The dl redirect handler

`internal/dl/`. Extracted verbatim from CLAUDE.md, no wording changed.

Download handler on `dl.{domain}/{project}` with version/branch resolution via query params. Redirects to static. Self-registering via init().

A `latest` redirect and a branch redirect are mutable pointers. They are served `Cache-Control: no-store`. A CDN therefore never pins clients to a stale release after a new publish. An exact-version redirect is an immutable mapping. It is `Cache-Control: public, max-age=31536000, immutable`.

A **private** project's redirect is always a `302` with `Cache-Control: private, no-store`. The route is ReadAccess-gated, so the caller authenticated. Its Location carries a short-lived signed download token (`static.SignedURL`, 15 min). A client drops the Authorization header when it follows the cross-host redirect to static. Those are curl's semantics, and Homebrew inherits them. The Location must therefore authorize itself. Never cache it. It embeds a live credential.

Platform-name aliases in `os`/`arch` (GitHub Actions' RUNNER_OS `Linux`/`macOS`/`Windows`, RUNNER_ARCH `X64`/`ARM64`, uname's `x86_64`/`aarch64`, ...) are folded to canonical via `db.NormalizeOS`/`db.NormalizeArch` before redirecting, so callers can pass platform names through verbatim.

## Canonical platform fold

One multi-platform artifact can cover a requested platform (one APE covers linux, darwin and windows). The redirect then carries the artifact's CANONICAL os/arch, not the requested pair. Every covered platform therefore shares one static URL, one digest and one ETag, instead of one CDN object per platform. A pair no artifact covers is left untouched, and static answers the 404. Depth: `docs/multi-platform-artifacts.md`.

**Naming NO platform asks for the artifact that runs anywhere.** `dl.{domain}/{project}` with no `os` and no `arch` resolves to the release's APE. It redirects to the same static URL every named platform already folds to. A caller on a machine the APE covers has nothing to say about its own platform. Making it say so is a parameter that changes no answer. This is not a download-time fallback. Nothing is guessed. The artifact is the one whose own bytes declared themselves portable at ingest (`artifacts.exe_format`).

A release that fans out per platform cannot answer such a request, and neither can one carrying several portable artifacts. Both are a 400 that NAMES the platforms the release does carry. The caller then reads what to ask for. Naming half the pair is a 400 too: that is a typo, not a request for the portable build.
