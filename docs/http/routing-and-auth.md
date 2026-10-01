# Routing, auth and the browse frontend

`internal/auth/`, `internal/web/`, `internal/llms/`. Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed.

## internal/auth

This package holds token auth, OIDC JWT verification, the centralized project-auth middleware (requireProject), the route registry and the RouteInfo interface. The registry offers Handle, HandleRaw and HandleHandler for a main-domain route. It offers ServiceHandle, ServiceHandleRaw, ServiceHandleHandler and ServiceRedirect for a subdomain route. `github.com/wow-look-at-my/router` backs it. A service registration is rewritten to a host and path pattern (`<sub>.{domain}/<path>`) on the single router. Dispatch and route listing therefore use the router's own host matching. There is no per-subdomain dispatch table.

`downloadtoken.go` mints and verifies **stateless, artifact-bound, expiring download tokens**. Each is an HMAC-SHA256 over `(project, version, os, arch, fmt, debug, expiry)` with a `bhdl_` prefix. The key persists at `{DataDir}/download-signing.key` and is generated on first start, like the APT key. The temporary-link endpoints use them. The file also holds `ApexServiceURL`, which derives the registry apex from any request Host by a strip of a known leading service or admin label. It is correct from the apex API too, where the unconditional-strip `DeriveServiceURL` is not.

`sso.go` implements the **cross-domain sign-in handoff** for the optional site domain. See the Browser sign-in security note. `SiteDomainHandle` and `SiteDomainHandleRaw` register `{project}.<site-domain>` routes. `siteApexOf` is the apex-classifier. The site domain, or exactly one label under it, is its OWN apex. `apexRootURL`, `apexHost`, `safeNextURL` and `ApexServiceURL` all honor that. `/__sso` redeems the one-time handoff codes `/__signin` mints on the primary apex (`BUILDHOST_PRIMARY_DOMAIN`).

A backend self-registers its routes from init() on auth.Router(). Adding a backend means adding files, and no existing file is modified. Each backend uses auth.ServiceHandle, auth.ServiceHandleRaw or auth.ServiceHandleHandler with a subdomain and a pattern, for host-based routing. The registry prefixes the subdomain and a `{domain}` host token to the pattern. The router then matches by Host, as in `apt.{domain}/{path...}`.

### Registration timing: init(), never OnReady

`auth.OnReady` runs from `auth.Init`, i.e. only in a booted server. A route registered there is absent from `buildhost routes`. The PR route-diff job therefore never shows it, and it can change with nobody seeing the change. Register routes in `init()`. Use OnReady only to wire handler dependencies: the DB, the store and the data dir. Method values on the package-level `handler` var bind a pointer, so a route registered in `init()` still sees fields OnReady assigns later.

A pattern that genuinely depends on configuration registers through `auth.OnSiteDomain` instead. `auth.Init` runs those with the configured site domain. `auth.ListRoutes`, which is what `buildhost routes` prints, runs them with `auth.SiteDomainPlaceholder`. The family therefore stays enumerable and diffable.

`internal/routescheck` enforces both mechanically: it diffs the enumerable route table against a booted one, so ANY route that appears only after `auth.Init` fails. There is no allowlist to keep up to date and nothing a new backend has to remember to do.

## internal/web

A public, read-only browse frontend on the main domain, with no subdomain. Go `html/template` renders the HTML on the server. The templates and the single `static/style.css` are embedded. There is **no JavaScript**. The registry is therefore indexable and consumable without a SPA. The routes are `GET /` (the public project index, which filters private projects the way `GET /api/v1/projects` does), `GET /projects/{project}`, `GET /projects/{project}/releases/{version}` and `GET /_ui/style.css`.

A project or release page registers through `auth.Handle` with `auth.HiddenReadAccess`. The shared `requireProject` middleware, the single home of project-auth, therefore enforces visibility. It returns a **`404`** and never a `401` for a private project the viewer may not see. That is indistinguishable from a project that does not exist, which is the GitHub style, and it leaks no existence. A read-scoped token authorized for the project reveals it. Only a published release is shown. A download link points at the `dl` subdomain (`dl.{host}/{project}?v=&os=&arch=&fmt=`). An install snippet mirrors llms.txt. The page handlers relax the global `default-src 'none'` CSP just enough for the one same-origin stylesheet (`style-src 'self'`). No `script-src` is ever emitted. It self-registers through init() for the routes and OnReady for the DB. It is distinct from `internal/admin/`, the authenticated admin SPA on a separate port.

## internal/llms

The public `/llms.txt` endpoint (https://llmstxt.org). It serves a plain-text guide to buildhost for an LLM or an agent. It renders per request from an embedded `template.md`, with the server's own base URL substituted in. That URL comes from the request `Host`. It is registered on the apex through `HandleRaw` **and on every service subdomain** through `ServiceHandleRaw`, for apt, brew, dl, npm, oci, sites and static. The router partitions hosts strictly, so a known subdomain never falls through to the host-agnostic apex route. Without the per-subdomain registration, `oci.{domain}/llms.txt` and `npm.{domain}/llms.txt` 404. `docker.{domain}/llms.txt` 301-redirects to `oci.{domain}`, like every other docker path.

The rendered guide always anchors its service URLs to the apex, whichever host served it. `apexBaseURL` strips a known leading service label, which mirrors the server's own first-label dispatch. A request on `oci.{domain}` therefore still renders `dl.{domain}` and not `dl.oci.{domain}`. It is public, with no auth. It self-registers through init().
