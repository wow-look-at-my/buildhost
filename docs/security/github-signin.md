# Browser sign-in with GitHub (per-repo authorization)

Extracted verbatim from CLAUDE.md; paragraph breaks were added at the existing
topic boundaries, no wording changed. CLAUDE.md carried this bullet TWICE, in two
versions that had drifted apart -- the older copy predated the dead-token re-auth
path. The newer, superset version is what follows; the stale copy was dropped.

## The flow

A browser hitting a private resource (e.g. a private static-site PR preview or
download) used to get a raw JSON `401` with no way to authenticate. When GitHub
login is configured (`BUILDHOST_GITHUB_CLIENT_ID` +
`BUILDHOST_GITHUB_CLIENT_SECRET`), `requireProject`'s `unauthorizedResponse`
(`internal/auth/middleware.go`) `303`-redirects a **browser navigation** (`Accept:
text/html`) with no token to the apex `/__signin?next=<full original URL>`, which
runs the GitHub OAuth Authorization Code flow
(`internal/auth/githublogin.go`). `handleSigninStart` sets a short-lived
signed-state + CSRF nonce cookie and redirects to GitHub with `scope=repo` (the
only classic OAuth scope that grants private-repo visibility, needed by the access
check below); `handleSigninCallback` (the fixed `redirect_uri`) verifies state +
nonce, exchanges the code for a user token, and reads the user's login (`GET
/user`).

## Callback failures are never dead ends

The browser is parked on the fixed callback URL, where a reload just re-submits
the consumed single-use code: every failure branch is logged via `slog` --
including GitHub's `error`/`error_description` from a failed exchange, never
codes/tokens/secrets -- and a *recoverable* failure (expired state; nonce cookie
missing/mismatched, e.g. overwritten by a second sign-in tab) `303`s back to
`/__signin?retry=1&next=<the state's own MAC-verified next>` to transparently
restart the flow. The `retry` marker rides the **signed state**
(`signinState.retried`), so a restarted flow that fails again renders the terminal
page instead of looping (and the marker never relaxes any validation).

Terminal failures -- forged/corrupt state, GitHub's `?error=`, a failed code
exchange or `GET /user` -- render a small `signinFailedHTML` 4xx page (one
sanitized sentence + a "Try signing in again" link to `/__signin` carrying the
verified next, or no next when the state was forged); **never a 5xx**, because
Cloudflare replaces origin 5xx bodies with its own bare error page, which used to
strand users at the callback with `error code: 502` and nothing in the logs.
Validation semantics (state MAC + expiry, double-submit nonce, `safeNextURL`) are
unchanged.

## The session cookie

It mints a buildhost-signed `bh_session` cookie carrying **login + the GitHub
token** (HMAC over `login\x00token|exp` with the shared `download-signing.key`;
HttpOnly, SameSite=Lax, Secure-on-https, 12h), set **domain-wide**
(`Domain=<apex>`) so one login on the single apex callback works across every
service subdomain.

## Authorization is per-repo, not an org allowlist

Each project records the GitHub `owner/repo` it was provisioned from
(`projects.github_repo`, set from the OIDC publish subject `repo:OWNER/NAME:...`
via `WithOIDCRepo` on create + re-synced on each write). `Authenticate` verifies
`bh_session` -> `WithUser(login)` + `WithGitHubToken`; `requireProject` calls
`userCanReadProject`, which grants read iff the signed-in user can access that
repo -- `GET /repos/{owner}/{repo}` with the user's token returns `200`
(`canAccessRepo`, cached per (login, repo, token-fingerprint) for 5m).

`checkRepoAccess` classifies the probe three ways: `200` = access, `404` =
definite no access (GitHub 404s a repo the token can't see), and `401` = **the
session's embedded token itself is dead** (revoked or expired mid-session -- on
this fixed-host authenticated GET, rate limiting is 403/429, never 401, so a 401
is unambiguous). The cache stores **only authoritative answers** -- 200, 404, or
the token-dead 401 (dead tokens never come back, and the fingerprint key means a
fresh sign-in is re-checked) -- so a *transient* failure (network error, 5xx, 429,
or a rate-limit 403) denies just the one request without being cached, instead of
pinning an authorized owner to "Access denied" for the whole TTL on a momentary
GitHub hiccup; and keying on a token fingerprint means a fresh re-login with a
broader-scoped token is never shadowed by a negative cached against the previous
token. Every non-200/404 probe outcome is logged at WARN with the repo and status.

Applies to ReadAccess and HiddenReadAccess; it never grants write (writes still
require a `write` token). A project with no recorded `github_repo` can't be opened
via GitHub login (token only).

## A dead session token re-auths transparently

When the access probe answers the token-dead `401`, the ReadAccess deny path marks
the request (`WithSessionTokenDead`) and `unauthorizedResponse` clears the
`bh_session` cookie (via `clearCookie`; `apexHost` strips a known service label so
the clear's Domain matches the apex Domain the cookie was set with, even from a
service subdomain) and `303`s the browser to `/__signin?next=<original URL>` --
loop-safe, because the OAuth callback mints a session only after `GET /user`
succeeds with the fresh token. Previously a 401 was lumped into the transient
bucket, so a browser whose 12h cookie outlived its GitHub token was dead-ended on
a misleading "Access denied" page until sign-out. Hidden reads
(`HiddenReadAccess`) deliberately keep the canonical 404 even for a dead session --
a re-auth redirect would leak that the hidden project exists.

## A signed-in but unauthorized browser gets an actionable 403

**A signed-in browser that is still unauthorized with a live token** (its GitHub
account can't read the backing repo, or the project has no `github_repo`) is NOT
redirected -- it already holds a valid session, so a `/__signin` bounce would loop
(GitHub re-auths the same account). Instead `unauthorizedResponse` renders an
actionable HTML `403` (`signedInForbiddenHTML`): it names the repo access required
and offers a "Sign out & switch account" link (apex `/__signout?next=<resource>`,
via `signoutURL`), so the user can re-authenticate as an account that has access --
replacing the dead-end JSON `401` a browser could not act on. (The resolved
project is put in the request context before the access switch so the page can
name the repo.) The page relaxes CSP to `default-src 'none'; style-src
'unsafe-inline'` for one inline `<style>`; no scripts.

`safeNextURL` accepts only a same-site path or an apex/subdomain URL (no open
redirect); `apexRootURL` derives the apex by stripping a known service label. The
whole flow is apex-only (`/__signin`, `/__signin/callback`, `/__signout` via
`HandleRaw`) since GitHub OAuth registers a single callback URL. When
unconfigured, browsers fall back to the plain JSON `401` (programmatic clients and
`/v2/` OCI are always unchanged). Public sites (`X-Public-Site: true`) are still
served with no auth at all.

## Setup

Create a GitHub OAuth App with callback `https://{apex}/__signin/callback`, set
`BUILDHOST_GITHUB_CLIENT_ID` + `BUILDHOST_GITHUB_CLIENT_SECRET`. (Trade-off: the
`repo` scope is broad and the user's token lives in the signed session cookie; a
GitHub App with fine-grained read-only repo permission would narrow this in a
future iteration.)

## Cross-domain sign-in handoff (site domain)

`myapp.<site-domain>` is a different REGISTRABLE domain from the primary apex, so
the `Domain=<apex>` `bh_session` cookie cannot cross, and the single OAuth App's
callback lives on the primary apex only. An unauthenticated site-domain browser is
303'd to `https://<BUILDHOST_PRIMARY_DOMAIN>/__signin?next=<original URL>` (no
primary domain configured => plain JSON 401, never a redirect to an apex that
cannot complete OAuth).

After sign-in there -- full OAuth, or instantly when the request already carries a
valid primary-apex session -- the minted session VALUE is parked server-side under
a random one-time nonce (`ssoHandoffs`, in-memory, TTL-swept; a restart voids
in-flight codes harmlessly) and the browser is 303'd to
`https://<next-host>/__sso?code=<signed nonce+next>&next=<original>`. The code is
HMAC-signed with the shared `download-signing.key` (purpose-separated), expires in
<=60s, is single-use (deleted on redemption), and BINDS the destination (a
mismatched `next` query param is rejected). **The session token itself never
appears in any URL** -- the code is worthless without the server-side entry
(asserted by tests: no token material in any Location header).

`/__sso` answers only on the site domain (its own Host gate 404s elsewhere; the
host-agnostic registration exists solely for the bare site apex fallthrough), sets
the parked session as a `Domain=<site-domain>` cookie via the same
`setSessionCookie` (apexHost classifies site hosts to the site apex), and 303s to
the destination; every handoff response is `Cache-Control: no-store` and every
failure is a 4xx page with a restart link at the primary apex (`signinFailedPage`,
shared with the callback -- never a 5xx).

Cross-site theft shapes: a crafted signin link to an attacker's public project site
only signs the VICTIM's own browser into the site domain (HttpOnly cookie,
unreadable to page JS; site responses use `Access-Control-Allow-Origin: *`, which
browsers never combine with credentials, so cross-origin credentialed reads stay
blocked) -- the same trust shape as arbitrary site JS on `sites.<apex>` under the
apex-wide cookie today.

## Unknown-domain exposure (scoped to the primary apex when configured)

With `BUILDHOST_PRIMARY_DOMAIN` set, the web frontend (`/`, `/projects/*`,
`/_ui/style.css`) and the WHOLE `/api/v1` surface answer only on that apex. They
register via `auth.HandlePrimary`/`auth.HandleRawPrimary`
(internal/auth/registry.go), whose `primaryOnly` gate compares the port-stripped,
case-folded request Host against the configured apex and serves the router's own
`http.NotFound` on any other unclaimed host -- BEFORE requireProject, so no auth
semantics (401s, sign-in redirects, OIDC auto-provisioning) ever run off-apex and
the 404 is byte-identical to a genuinely unregistered path (no "buildhost but
wrong host" fingerprint; pinned by `TestPrimaryDomain_ScopesWebAndAPIToApex`).

Deliberately still host-agnostic on every unclaimed host (incl. the bare site apex
and stray CNAMEs): `/healthz` and `/ready-to-update` (container-internal probes
address the server by container DNS/localhost), `/llms.txt`, the sign-in flow
(`/__signin`, `/__signin/callback`, `/__signout`), `/__sso` (bare-site-apex
redemption relies on the fallthrough), and the `/npm/*` -> `npm.{domain}`
redirect. With `BUILDHOST_PRIMARY_DOMAIN` unset the historical fully-host-agnostic
behavior is byte-identical (IP/localhost deployments unchanged), and the gate is
serve-time only -- configuring a primary domain never changes the route table
(both pinned by `TestPrimaryDomain_UnsetKeepsHostAgnostic`).
