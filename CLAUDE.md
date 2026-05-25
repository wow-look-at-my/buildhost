# buildhost

Universal package registry server. Upload artifacts once, download in any format.

## Build

```bash
go-toolchain
```

This runs mod tidy, vet, tests with coverage, and builds the binary. Do not use bare `go` commands.

## Project structure

- `cmd/buildhost/` - CLI entrypoint (cobra, one subcommand per file, self-registering via init()). Backend imports (backend_*.go) trigger init() registration for each handler package.
- `internal/server/` - HTTP server, global middleware chain (auth, inflight tracking, security headers, logging, recovery)
- `internal/api/` - REST API handlers (projects, releases, artifacts, publish, tokens). Each handler file registers its own routes via auth.OnReady().
- `internal/static/` - Unified download endpoint on `static.{domain}/file`. All artifact downloads go through here. Fmt interface with self-registration; query params: `project`, `v`, `os`, `arch`, `fmt`. Includes raw/symbols formats and a bridge for repackage-based formats.
- `internal/dl/` - Download handler on `dl.{domain}/{project}` with version/branch resolution via query params. Redirects to static. Self-registering via auth.OnReady().
- `internal/apt/` - APT repository endpoint on `apt.{domain}/{project}/...`. Pool downloads redirect to static. Self-registering via auth.OnReady().
- `internal/brew/` - Homebrew tap endpoint on `brew.{domain}/{project}`. Formula download URLs point to static. Self-registering via auth.OnReady().
- `internal/npm/` - npm registry endpoint on `npm.{domain}/@buildhost/{project}`. Tarball URLs point to static. Self-registering via auth.OnReady().
- `internal/oci/` - OCI distribution endpoint on `oci.{domain}/v2/{project}/...`. `docker.{domain}` permanently redirects to `oci.{domain}`. Self-registering via auth.OnReady().
- `internal/sites/` - Static site hosting endpoint on `sites.{domain}/{project}/...`. Upload tar.gz archives, serve files per branch or version. Self-registering via auth.OnReady().
- `internal/auth/` - Token auth, OIDC JWT verification, centralized project-auth middleware (requireProject), route registry (Handle/HandleRaw/HandleHandler/ServiceRoute/ServiceRedirect), RouteInfo interface
- `internal/db/` - SQLite database layer (modernc.org/sqlite, no CGo), OIDC policy storage. Types (Project, Release, Artifact, APIToken, OIDCPolicy) and validation functions live here. Uses sqlc for query generation from `internal/db/queries/*.sql` with schema in `internal/db/schema.sql`.
- `internal/db/queries/` - SQL query files for sqlc code generation
- `internal/db/schema.sql` - SQLite schema used by sqlc
- `internal/storage/` - Content-addressed blob storage (filesystem backend, zstd-compressed, key validation)
- `internal/repackage/` - On-demand repackaging and stripping (tar.gz, tar.xz, tar.zst, zip, deb, brew, npm, oci). Self-registering via init(); Generator uses registry. Orchestrator just publishes releases.
- `internal/strip/` - Binary debug info stripping (shells out to strip/objcopy)
- `internal/version/` - Version resolution logic
- `internal/admin/` - Admin dashboard (separate HTTP server, JSON API + static SPA frontend), inflight write counter for update coordination
- `internal/config/` - Server configuration from env vars
- `migrations/` - SQLite schema (embedded via go:embed)

## Key design decisions

- Versioning: auto-increment (default) or semver (opt-in per project)
- Git branch is a first-class field on releases, not just metadata
- Repackaging and stripping happen on-demand at download time, not at publish time. Only the original upload is stored.
- All artifact downloads go through `static.{domain}/file?project=&v=&os=&arch=&fmt=` -- a single CDN-cacheable endpoint with sorted query params, strong ETags, and immutable cache headers. Format handlers (dl, apt, brew, npm) redirect to static after resolving version/branch. `v=latest` returns 400 (callers must resolve first). Repackage formats self-register via `Fmt` interface.
- Storage is content-addressed (SHA-256) with zstd compression and deduplication
- Auth: Bearer token, Basic auth, or query param — all resolve to the same token system
- OIDC: JWT-based auth for GitHub Actions (and any OIDC provider), keys fetched from issuer's JWKS endpoint
- OIDC auto-provisioning: trusted issuers (BUILDHOST_OIDC_ISSUERS) can create projects on first publish -- project name derived from JWT subject claim, org allowlist (BUILDHOST_OIDC_ORGS, use `*` to allow all), event allowlist (BUILDHOST_OIDC_EVENTS, defaults to `push` to limit to repo members)
- Private projects require auth on all endpoints including format-specific ones (APT, Brew, NPM, OCI)
- Project auth enforced once in centralized requireProject middleware — handlers never check auth
- Each backend defines a RouteInfo implementation (private route struct) for full URL parsing
- Backends self-register routes via auth.OnReady() on auth.Mux(); adding a backend = adding files, no existing files modified. Each backend uses auth.ServiceRoute(subdomain, pattern) for host-based routing.
- Tokens are project-scoped or global; project-scoped tokens cannot escalate privileges
- Token expiry is enforced at lookup time
- Default token scope is "read" (least privilege)
- Upload size capped at 2 GiB; JSON endpoints capped at 1 MiB
- Storage keys validated as hex SHA-256 to prevent path traversal
- Static sites: uploaded as tar.gz, stored as raw tar in content-addressed storage, served by scanning tar headers per request. Each branch is an independent deployment (one row in `sites` table). Re-deploying a branch replaces the previous site atomically. Upload size capped at 256 MiB, max 10,000 files per site.

## First-time setup

```bash
buildhost bootstrap          # Creates initial admin token (only works when no tokens exist)
buildhost bootstrap --name admin-token
```

## Running

```bash
BUILDHOST_LISTEN_ADDR=:8080 BUILDHOST_BASE_URL=https://example.com BUILDHOST_DOMAIN=example.com buildhost serve
```

`BUILDHOST_DOMAIN` derives subdomain URLs for each service: `apt.example.com`, `brew.example.com`, `npm.example.com`, `oci.example.com` (canonical, `docker.example.com` 301-redirects), `dl.example.com`, `sites.example.com`, `static.example.com`. API routes stay on the main domain.

Individual service URLs can be overridden with `BUILDHOST_<SERVICE>_URL` env vars (e.g., `BUILDHOST_STATIC_URL=https://cdn.example.com`). This allows subdomains, path-based routing, or completely different domains per service. Supported services: `APT`, `BREW`, `DL`, `NPM`, `OCI`, `SITES`, `STATIC`.

To disable application-level zstd compression (e.g., on ZFS or Btrfs with filesystem-level compression):

```bash
BUILDHOST_STORAGE_COMPRESS=false buildhost serve
```

The admin dashboard starts automatically on a separate port (default `:9090`). Set `BUILDHOST_ADMIN_LISTEN_ADDR` to change the address, or set it to empty to disable.

```bash
BUILDHOST_ADMIN_LISTEN_ADDR=:9090 buildhost serve   # listen on all interfaces (default)
BUILDHOST_ADMIN_LISTEN_ADDR= buildhost serve         # disable admin dashboard
```

## Telemetry (OpenTelemetry)

Set `BUILDHOST_OTEL_ENDPOINT` to enable distributed tracing and log export via OTLP/HTTP:

```bash
BUILDHOST_OTEL_ENDPOINT=https://otel.example.com buildhost serve
```

When set, the server exports:
- **Traces** to `{endpoint}/v1/traces` -- every HTTP request gets a root span, with child spans for DB queries (`db.exec`, `db.query`, `db.query_row`), storage operations (`storage.put`, `storage.get`, `storage.delete`, `storage.exists`), auth (OIDC verification), repackaging (`repackage.generate`), and download resolution (`dl.serve_artifact`).
- **Logs** to `{endpoint}/v1/logs` -- all slog output is bridged to OTEL with trace/span correlation.

Spans include attributes like `project.name`, `auth.type`, `http.method`, `url.path`, `http.status_code`, `db.statement`, `storage.key`, `storage.size`, `repackage.format`, etc.

When `BUILDHOST_OTEL_ENDPOINT` is unset (default), tracing is fully disabled with zero overhead (noop tracer).

## Graceful shutdown and update coordination

The server handles SIGTERM/SIGINT by calling `http.Server.Shutdown` with a 5-minute timeout, allowing in-flight requests (especially large uploads) to complete before the process exits.

For zero-downtime updates, use docker-updater's rolling update mode (`docker-updater.rolling: "true"`) with an nginx sidecar. docker-updater starts the new container before stopping the old one; nginx routes via Docker DNS. See `deploy/` for an example compose stack.

### Ready-to-update endpoint

`GET /ready-to-update` on the main server (`:8080`) returns HTTP 200 when the server is idle, or HTTP 503 when there are in-flight write requests. This is designed for docker-updater's HTTP pre-update checks -- docker-updater can query this endpoint directly (via the container's IP from the host network) without joining the container's network or exec'ing a process.

The `try-update` CLI subcommand wraps this endpoint for manual use or other pre-update hooks:

```bash
buildhost try-update                    # queries localhost:8080/ready-to-update
buildhost try-update --addr :9090       # custom listen address
```

Exit 0 means idle (safe to update); non-zero means busy or unreachable (skip this poll cycle).

The admin endpoint `GET /admin/inflight` on `:9090` still returns `{"inflight": N}` with the raw count for dashboards.

Docker Compose label configuration for docker-updater with rolling updates:

```yaml
labels:
  docker-updater.enable: "true"
  docker-updater.rolling: "true"
stop_grace_period: 5m
```

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

## Code generation

`internal/api/gen_project_name.go` is generated by [go-regex-compiler](https://github.com/wow-look-at-my/go-regex-compiler). The `go:generate` directive lives in `internal/api/projects.go`.

Database query code in `internal/db/*.sql.go` is generated by [sqlc](https://sqlc.dev/). Config is in `sqlc.yaml`. To regenerate after editing queries in `internal/db/queries/`:

```bash
sqlc generate
```

## Testing

`go-toolchain` runs all tests. Integration tests use httptest.NewServer with a temp SQLite DB.
OIDC tests generate ephemeral RSA keys and run a local JWKS server.

## Docker image

The image is built from `gcr.io/distroless/static-debian12:nonroot`. It runs as UID 65532 (nonroot) with no shell, no package manager, and no writable paths except the data volume. The server handles SIGTERM for graceful shutdown.

The admin dashboard on `:9090` has **no built-in authentication**. It must be placed behind a reverse proxy with access control (e.g., Cloudflare Access on a separate hostname). Never expose port 9090 to untrusted networks.

Binary stripping (`strip`/`objcopy`) is not available in the hardened image. The `strip.Available()` check handles this gracefully -- binaries are served as-is. Run stripping in your CI pipeline before uploading if needed.

All temporary files are written to `BUILDHOST_DATA_DIR/tmp`, not to `/tmp`. The image is compatible with `read_only: true` as long as the data volume is mounted.

## Security notes (for future security reviews)

The following items have been reviewed and addressed or are intentional design choices:

- **Rate limiting**: Handled at the reverse proxy layer, not in the application
- **OIDC SSRF**: jwks_uri is validated to match the issuer's host and require HTTPS (loopback exempted for tests)
- **OIDC issuer scheme**: fetchJWKS requires HTTPS for non-loopback issuers
- **Token in query param**: Intentional for clients that cannot set headers (APT, Brew). Mitigated by Referrer-Policy: no-referrer
- **No TLS termination**: Intentional -- runs behind a reverse proxy in Docker
- **Strip temp file permissions**: Runs in a single-user Docker container; permissions are 0600 anyway
- **APT Release signing**: Not yet implemented (TODO in code). Clients must use `[trusted=yes]`
- **List endpoints**: No LIMIT -- all behind auth, SQLite serialized, not a DoS vector
- **Symlink rejection**: Storage layer rejects symlinks via Lstat check
- **Admin dashboard auth**: None -- must be behind a reverse proxy with access control (Cloudflare Access, etc.)
- **Container user**: Runs as nonroot (UID 65532) via distroless base image
- **Graceful shutdown**: Server handles SIGTERM/SIGINT with 5-minute timeout for clean connection draining
- **Ready-to-update endpoint**: `GET /ready-to-update` on :8080 returns 200/503 with no body content -- reveals only idle/busy state, no sensitive data
- **Inflight endpoint**: `GET /admin/inflight` on :9090 is unauthenticated -- same trust model as the rest of the admin dashboard (internal-only, behind reverse proxy)
- **No writes outside data dir**: Temp files use BUILDHOST_DATA_DIR/tmp, not system /tmp
- **OIDC audience check**: Auto-provisioning verifies the token's `aud` claim matches `BUILDHOST_BASE_URL`. GHA workflows must request tokens with the buildhost URL as the audience: `core.getIDToken('https://buildhost.example.com')`
- **OIDC auto-provisioning**: Trusted issuers can auto-create projects. Project name derived from subject claim (repo:org/name:* -> name), lowercased and validated against `[a-z0-9][a-z0-9._-]{0,127}`. Scoped to read,write on that project only -- cannot access other projects. Optional BUILDHOST_OIDC_ORGS allowlist restricts which orgs can auto-provision
- **OIDC_ORGS wildcard risk**: Setting `BUILDHOST_OIDC_ORGS=*` allows any GitHub org to auto-provision projects. Since project names are derived from repo names, any repo in any org with the same name as an existing project would derive the same project name. The first push creates the project; subsequent pushes from other orgs are blocked by `AuthorizedForProjectName`. However, avoid `BUILDHOST_OIDC_ORGS=*` in production -- scope the allowlist to trusted orgs only
