# OIDC trust model

Extracted verbatim from CLAUDE.md. Paragraph breaks were added at the existing topic boundaries, no wording changed.

JWT-based auth for GitHub Actions (and any OIDC provider), keys fetched from the issuer's JWKS endpoint.

## Auto-provisioning

Trusted issuers (BUILDHOST_OIDC_ISSUERS) can create projects on first publish -- project name derived from JWT subject claim. A repo's token is authorized for its own project and any `<repo>/<...>` sub-namespace (multi-binary repos publish each binary to `<repo>/<binary>`, e.g. `log-streamer/client`). Org allowlist (BUILDHOST_OIDC_ORGS, matched case-insensitively, use `*` to allow all), event allowlist (BUILDHOST_OIDC_EVENTS, defaults to `push,pull_request,workflow_dispatch` -- all three imply write access to the repo: a push/same-repo PR comes from a member.

Project name derived from subject claim (repo:org/name:* -> name), lowercased and validated against `[a-z0-9][a-z0-9._-]{0,127}`. Authorized for read,write on that repo's whole namespace -- project `R` plus any slash-namespaced `R/<...>` beneath it (so a multi-binary repo publishes `R/<binary>` for each binary), gated by a trailing-slash boundary so sibling prefixes. `requireProject` validates an auto-created namespaced name per-segment before creating it. Optional BUILDHOST_OIDC_ORGS allowlist restricts which orgs can auto-provision.

**Provisioning is write-only**: `requireProject` only creates a missing project for a `WriteAccess` route (the publish POST/PUT flow, docker push, site deploy). A read (`ReadAccess`/`HiddenReadAccess`: dl/static/apt/brew/npm and the web frontend) never provisions -- a GET 404s instead, so it can never materialize a project as a side effect.

**OIDC_ORGS wildcard risk**: Setting `BUILDHOST_OIDC_ORGS=*` allows any GitHub org to auto-provision projects. Since project names are derived from repo names, any repo in any org with the same name as an existing project will derive the same. The first push creates the project. Subsequent pushes from other orgs are blocked by `AuthorizedForProjectName`. However, avoid `BUILDHOST_OIDC_ORGS=*` in production -- scope the allowlist to trusted orgs only.

## Audience check

The auto-provisioning path does NOT gate on the token's `aud` claim -- trust for a trusted-issuer token comes from the JWKS signature plus the org allowlist. (Telling the server its own URL was never a meaningful trust boundary, and a stale/missing value caused a production 401 outage, so the gate was removed.) A per-policy `audience` field on an `OIDCPolicy` is still honored as an optional, opt-in restriction for explicitly configured policies. The server is never told its own URL: generated links are derived per request from the `Host` header (`auth.RequestBaseURL`).

## Event check

Tokens without an `event_name` claim are rejected when `BUILDHOST_OIDC_EVENTS` is configured (default: `push,pull_request,workflow_dispatch`). This prevents bypass via providers that omit the claim. The default set is deliberately limited to events that imply the actor has write access to the repo: fork PRs in GitHub Actions do not.

## Repo-identity pinning (rename/resurrection guard)

GitHub owner/repo NAMES are reusable -- delete or rename a repo and a stranger can re-register the name and mint valid OIDC tokens for the same. Verification prefers the dedicated claims (`repository`, `repository_owner`, `*_id`) over subject parsing and falls back to the subject's `@id` suffixes (`splitImmutableID`. Names keep matching allowlists/projects, classic subjects parse byte-identically).

The middleware pins the IDs on the project (`projects.github_owner_id`/`github_repo_id`, migration 014): recorded at provisioning, or on the first ID-bearing PUBLISH for pre-existing projects (trust on first use. Reads never mutate). Any later OIDC request whose token carries DIFFERENT IDs is refused -- 403 with an explicit "renamed or re-created (resurrected) repository may not take over.

Tokens WITHOUT IDs (issuers minting neither the claims nor immutable subjects) are deliberately not rejected: they already passed the issuer/org/event gates, and GitHub mints ID claims for all repos anyway. `BUILDHOST_OIDC_ORGS` entries may optionally pin the org's account ID as `name@id` (matches by name AND id, refusing ID-less tokens). Plain-name entries keep matching any id, with the per-project pin providing the takeover protection. An operator repointing a project at a legitimately re-created repo must clear/re-pin the recorded IDs by hand (deliberate).

## Smaller items

- **OIDC SSRF**: jwks_uri is validated to match the issuer's host and require HTTPS (loopback exempted for tests)
- **OIDC issuer scheme**: fetchJWKS requires HTTPS for non-loopback issuers
- **OIDC RSA key size**: JWKS keys below 2048 bits are rejected
- **OIDC visibility sync**: When an OIDC token's `repository_visibility` claim changes project visibility, the change is logged at WARN level with project name, old/new visibility.
