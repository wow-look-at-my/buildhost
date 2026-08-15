# Running the server

Extracted verbatim from CLAUDE.md, no wording changed.

## First-time setup

```bash
buildhost bootstrap          # Creates initial admin token (only works when no tokens exist)
buildhost bootstrap --name admin-token
```

## Listing routes

```bash
buildhost routes   # prints all registered HTTP routes, sorted
```

Routes are printed exactly as registered. Main-domain routes are path-only
(`/api/v1/projects {GET,POST}`, `/healthz {GET}`); subdomain routes carry their
service label and the `{domain}` host token (`apt.{domain}/{path...} {GET}`,
`oci.{domain}/v2/ {*}`, `static.{domain}/file {GET}`). Nothing is synthesized at
listing time -- the `{domain}` token is the real host wildcard the router matches.

## Serving

```bash
BUILDHOST_LISTEN_ADDR=:8080 buildhost serve
```

Each service is accessed via a subdomain derived from the incoming request's
`Host` header: `apt.example.com`, `brew.example.com`, `git.example.com`,
`npm.example.com`, `oci.example.com` (canonical, `docker.example.com`
301-redirects), `dl.example.com`, `goproxy.example.com`, `sites.example.com`,
`static.example.com`. API
routes stay on the main domain. No domain configuration is required -- the server
dispatches by matching the first label of the Host header against known service
names.

Two optional domain envs exist for the project-site-subdomain scheme (see
`docs/sites.md`): `BUILDHOST_SITE_DOMAIN` (serve each DNS-label-valid project's
site at `https://<project>.<domain>/`; unset = zero new routes) and
`BUILDHOST_PRIMARY_DOMAIN` (the apex carrying the GitHub OAuth callback, the
sign-in target for private site-domain resources; unset while SITE_DOMAIN is set =
site-domain browsers get the plain JSON 401; setting it also scopes the web UI and
`/api/v1` to that apex -- see `docs/security/github-signin.md`). DNS for the site
domain needs the apex plus a wildcard `*.<domain>` pointing at the same origin
with Host passthrough, and a wildcard TLS cert at the proxy.

## Go module proxy

`goproxy.{domain}` serves the Go module download protocol. It needs no
configuration to run: private module prefixes default to `github.com/<org>` for
each `BUILDHOST_OIDC_ORGS` entry, and it uses buildhost's existing GitHub
credential (`BUILDHOST_GITHUB_APP_ID` + `BUILDHOST_GITHUB_APP_PRIVATE_KEY`, else
`BUILDHOST_GITHUB_TOKEN`).

**Set `BUILDHOST_GOPROXY_READINESS_MODULE` to a private module.** Without it the
readiness check can only confirm a credential EXISTS, and a credential that
authenticates but is not authorized for the org looks identical to a working one
-- which is exactly how a proxy serving zero private modules went unnoticed. With
it set, the check resolves that module every 15 minutes and reports unready when
it cannot. `BUILDHOST_GOPROXY_PRIVATE_PREFIXES` and `BUILDHOST_GOPROXY_UPSTREAM`
override the defaults. Depth: `docs/formats/goproxy.md`.

To disable application-level zstd compression (e.g., on ZFS or Btrfs with
filesystem-level compression):

```bash
BUILDHOST_STORAGE_COMPRESS=false buildhost serve
```

The admin dashboard starts automatically on a separate port (default `:9090`). Set
`BUILDHOST_ADMIN_LISTEN_ADDR` to change the address, or set it to empty to
disable.

```bash
BUILDHOST_ADMIN_LISTEN_ADDR=:9090 buildhost serve   # listen on all interfaces (default)
BUILDHOST_ADMIN_LISTEN_ADDR= buildhost serve         # disable admin dashboard
```

## Telemetry (OpenTelemetry)

Set `BUILDHOST_OTEL_ENDPOINT` to enable distributed tracing and log export via
OTLP/HTTP:

```bash
BUILDHOST_OTEL_ENDPOINT=https://otel.example.com buildhost serve
```

When set, the server exports:

- **Traces** to `{endpoint}/v1/traces` -- every HTTP request gets a root span,
  with child spans for DB queries (`db.exec`, `db.query`, `db.query_row`), storage
  operations (`storage.put`, `storage.get`, `storage.delete`, `storage.exists`),
  auth (OIDC verification), repackaging (`repackage.generate`), and download
  resolution (`dl.serve_artifact`).
- **Logs** to `{endpoint}/v1/logs` -- all slog output is bridged to OTEL with
  trace/span correlation.

Spans include attributes like `project.name`, `auth.type`, `http.method`,
`url.path`, `http.status_code`, `db.statement`, `storage.key`, `storage.size`,
`repackage.format`, etc.

When `BUILDHOST_OTEL_ENDPOINT` is unset (default), tracing is fully disabled with
zero overhead (noop tracer).

## OIDC for GitHub Actions

Configure an OIDC policy so GHA workflows can authenticate without static tokens:

```bash
# Create a policy that grants read,write to project ID 1 for any workflow in myorg/myrepo
curl -X POST https://buildhost.example.com/api/v1/oidc/policies \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"issuer":"https://token.actions.githubusercontent.com","subject_pattern":"repo:myorg/myrepo:*","project_id":1,"scopes":"read,write"}'
```

In the GHA workflow, request an OIDC token and use it as a Bearer token:

```yaml
permissions:
  id-token: write
steps:
  - uses: actions/github-script@v7
    id: token
    with:
      script: return await core.getIDToken('https://buildhost.example.com')
  - run: |
      curl -H "Authorization: Bearer ${{ steps.token.outputs.result }}" \
        https://buildhost.example.com/api/v1/projects
```
