# Routing, auth and the browse frontend

`internal/auth/`, `internal/web/`, `internal/llms/`. Extracted verbatim from
CLAUDE.md; paragraph breaks were added at the existing topic boundaries, no
wording changed.

## internal/auth

Token auth, OIDC JWT verification, centralized project-auth middleware
(requireProject), route registry (Handle/HandleRaw/HandleHandler for main-domain
routes; ServiceHandle/ServiceHandleRaw/ServiceHandleHandler/ServiceRedirect for
subdomain routes) backed by `github.com/wow-look-at-my/router`, RouteInfo
interface. Service registrations are rewritten to host+path patterns
(`<sub>.{domain}/<path>`) on the single router, so dispatch and route listing use
the router's own host matching -- there is no per-subdomain dispatch table.

`downloadtoken.go` mints/verifies **stateless, artifact-bound, expiring download
tokens** (HMAC-SHA256 over `(project, version, os, arch, fmt, debug, expiry)` with
a `bhdl_` prefix; key persisted at `{DataDir}/download-signing.key`, generated on
first start like the APT key) used by the temporary-link endpoints, plus
`ApexServiceURL` (derives the registry apex from any request Host by stripping a
known leading service/admin label -- correct from the apex API too, unlike the
unconditional-strip `DeriveServiceURL`).

`sso.go` implements the **cross-domain sign-in handoff** for the optional site
domain (see the Browser sign-in security note): `SiteDomainHandle`/
`SiteDomainHandleRaw` register `{project}.<site-domain>` routes, `siteApexOf` is
the apex-classifier (the site domain, or exactly one label under it, is its OWN
apex -- honored by `apexRootURL`, `apexHost`, `safeNextURL`, and
`ApexServiceURL`), and `/__sso` redeems one-time handoff codes minted by
`/__signin` on the primary apex (`BUILDHOST_PRIMARY_DOMAIN`).

Backends self-register routes via auth.OnReady() on auth.Router(); adding a
backend = adding files, no existing files modified. Each backend uses
auth.ServiceHandle/ServiceHandleRaw/ServiceHandleHandler(subdomain, pattern, ...)
for host-based routing; the registry prefixes the subdomain and a `{domain}` host
token to the pattern so the router matches by Host (e.g. `apt.{domain}/{path...}`).

## internal/web

Public, read-only browse frontend on the main domain (no subdomain).
Server-rendered HTML via Go `html/template` (templates and the single
`static/style.css` are embedded); **no JavaScript**, so the registry is
indexable/consumable without a SPA. Routes: `GET /` (public project index, private
projects filtered like `GET /api/v1/projects`), `GET /projects/{project}`, `GET
/projects/{project}/releases/{version}`, and `GET /_ui/style.css`.

Project/release pages register via `auth.Handle` with `auth.HiddenReadAccess`, so
the shared `requireProject` middleware (the single home of project-auth) enforces
visibility and returns a **`404`** -- never a `401` -- for a private project the
viewer may not see, indistinguishable from a project that does not exist
(GitHub-style; no existence leak). A read-scoped token authorized for the project
reveals it. Only published releases are shown. Download links point at the `dl`
subdomain (`dl.{host}/{project}?v=&os=&arch=&fmt=`); install snippets mirror
llms.txt. The page handlers relax the global `default-src 'none'` CSP just enough
for the one same-origin stylesheet (`style-src 'self'`); no `script-src` is ever
emitted. Self-registering via init() (routes) + OnReady (DB). Distinct from
`internal/admin/` (the authenticated admin SPA on a separate port).

## internal/llms

Public `/llms.txt` endpoint (https://llmstxt.org). Serves a plain-text guide to
buildhost for LLMs/agents, rendered per request from an embedded `template.md`
with the server's own base URL (derived from the request `Host`) substituted in.
Registered on the apex (`HandleRaw`) **and on every service subdomain**
(`ServiceHandleRaw` for each of apt/brew/dl/npm/oci/sites/static) -- the router's
strict host partitioning means a known subdomain never falls through to the
host-agnostic apex route, so without per-subdomain registration
`oci.{domain}/llms.txt`, `npm.{domain}/llms.txt`, etc. would 404.
`docker.{domain}/llms.txt` 301-redirects to `oci.{domain}` like every other docker
path. The rendered guide always anchors its service URLs to the apex regardless of
which host served it (`apexBaseURL` strips a known leading service label,
mirroring the server's own first-label dispatch), so a request on `oci.{domain}`
still renders `dl.{domain}` rather than `dl.oci.{domain}`. Public (no auth).
Self-registering via init().
