# OIDC trust model

Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed.

JWT-based auth for GitHub Actions, and for any OIDC provider. The keys come from the issuer's JWKS endpoint.

## Auto-provisioning

A trusted issuer (`BUILDHOST_OIDC_ISSUERS`) can create a project on the first publish. The project name comes from the JWT subject claim. A repo's token is authorized for its own project and for any `<repo>/<...>` sub-namespace. A multi-binary repo therefore publishes each binary to `<repo>/<binary>`, such as `log-streamer/client`.

Two allowlists gate this path. `BUILDHOST_OIDC_ORGS` is the org allowlist. It matches case-insensitively, and `*` allows every org. `BUILDHOST_OIDC_EVENTS` is the event allowlist. It defaults to `push,pull_request,workflow_dispatch`.

All three default events imply write access to the repo. A push comes from a member. A fork PR gets no OIDC token, so `pull_request` means a same-repo PR. Only a user with repo write access can trigger a manual `workflow_dispatch` run.

The project name comes from the subject claim (`repo:org/name:*` gives `name`). It is lowercased and validated against `[a-z0-9][a-z0-9._-]{0,127}`. The token is authorized to read and write that repo's whole namespace. That namespace is project `R` plus any slash-namespaced `R/<...>` beneath it. A trailing-slash boundary gates the match, so a sibling prefix such as `R-evil` is refused. An unrelated project is refused too. `requireProject` validates an auto-created namespaced name per segment before it creates the project.

**Provisioning is write-only.** `requireProject` creates a missing project only for a `WriteAccess` route. Those routes are the publish POST and PUT flow, the docker push, and the site deploy. A read never provisions. `ReadAccess` and `HiddenReadAccess` cover `dl`, `static`, `apt`, `brew`, `npm` and the web frontend. A GET returns 404 instead, so a read can never materialize a project as a side effect.

**The `BUILDHOST_OIDC_ORGS` wildcard carries a risk.** A value of `*` lets any GitHub org auto-provision a project. Project names come from repo names. A repo in any org therefore derives the same project name as an existing project of the same name. The first push creates the project. `AuthorizedForProjectName` blocks every later push from another org. Avoid `*` in production. Scope the allowlist to trusted orgs only.

## Audience check

The auto-provisioning path does NOT gate on the token's `aud` claim. Trust for a trusted-issuer token comes from the JWKS signature, the org allowlist (`BUILDHOST_OIDC_ORGS`), the event allowlist (`BUILDHOST_OIDC_EVENTS`), and the subject claim. To tell the server its own URL was never a meaningful trust boundary. A stale or missing value also caused a production 401 outage, so the gate was removed. A per-policy `audience` field on an `OIDCPolicy` is still honored. It is an optional, opt-in restriction for an explicitly configured policy. The server is never told its own URL. Every generated link comes from the request `Host` header (`auth.RequestBaseURL`).

## Event check

A token without an `event_name` claim is rejected when `BUILDHOST_OIDC_EVENTS` is configured. The default is `push,pull_request,workflow_dispatch`. This blocks a bypass through a provider that omits the claim.

The default set holds only events that imply the actor has write access to the repo. A fork PR in GitHub Actions receives no OIDC token, so `pull_request` means a same-repo PR from a member. GitHub lets only a user with write access trigger a manual `workflow_dispatch` run. `workflow_dispatch` therefore carries the same write-access guarantee as `push`. It is safe to include by default. It also makes a manual release or publish dispatch auto-provision out of the box.

## Repo-identity pinning (rename/resurrection guard)

A GitHub owner name and repo name are reusable. A stranger can delete or rename a repo, re-register the name, and mint a valid OIDC token for the same `owner/repo`. GitHub's numeric IDs are not reusable. An immutable subject claim therefore carries them (`repo:OWNER@OWNERID/REPO@REPOID:...`), for a repo created after the immutable-subject rollout. The long-standing `repository_id` and `repository_owner_id` claims carry them too.

Verification prefers the dedicated claims (`repository`, `repository_owner`, and the `*_id` pair) over subject parsing. It falls back to the subject's `@id` suffixes (`splitImmutableID`). A name still matches an allowlist and a project. A classic subject parses byte-identically.

The middleware pins the IDs on the project (`projects.github_owner_id` and `github_repo_id`, migration 014). It records them at provisioning. For a pre-existing project it records them on the first ID-bearing PUBLISH, which is trust on first use. A read never mutates them.

Any later OIDC request whose token carries a DIFFERENT REPO id is refused. The answer is a 403 with an explicit "renamed or re-created (resurrected) repository may not take over an existing project" error. A `HiddenReadAccess` route answers with the canonical 404 instead, so existence still never leaks. The refusal covers a read and a write. A resurrected repo can therefore neither publish to nor read a private predecessor's project.

The repo id alone decides that. It is the identifier of the repository itself. It survives a rename and a TRANSFER to another owner. It changes only when somebody deletes the repository and makes it again. A token whose OWNER id moved under an unchanged repo id is therefore the same repository under a new owner. Such a request is allowed. A write moves the owner pin with it, and logs "OIDC repo transfer re-pinned". A read is allowed and pins nothing. To refuse that case makes every ordinary org transfer need a hand edit of the database.

A token WITHOUT IDs is deliberately not rejected. Such an issuer mints neither the claims nor an immutable subject. The token already passed the issuer, org and event gates. GitHub mints ID claims for every repo anyway.

A `BUILDHOST_OIDC_ORGS` entry may optionally pin the org's account ID as `name@id`. That form matches by name AND id, and it refuses an ID-less token. A plain-name entry keeps matching any id. The per-project pin provides the takeover protection there.

An operator who repoints a project at a legitimately RE-CREATED repo must clear or re-pin the recorded IDs by hand. That is deliberate. A re-created repo has a new repo id, which is exactly what the guard cannot tell apart from a takeover. A transfer needs no such edit.

## Smaller items

- **OIDC SSRF**: `jwks_uri` must match the issuer's host and must use HTTPS. Loopback is exempt, for tests.
- **OIDC issuer scheme**: `fetchJWKS` requires HTTPS for a non-loopback issuer.
- **OIDC RSA key size**: a JWKS key below 2048 bits is rejected.
- **OIDC visibility sync**: a change of project visibility from a token's `repository_visibility` claim is logged at WARN level. The log line carries the project name, the old and new visibility, and the OIDC subject.
