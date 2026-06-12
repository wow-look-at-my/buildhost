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
- `internal/static/` - Unified download endpoint on `static.{domain}/file`. All artifact downloads go through here. Fmt interface with self-registration; query params: `project`, `v`, `os`, `arch`, `fmt`. Includes raw/symbols formats and a bridge for repackage-based formats. Also accepts a `token` query param carrying a **signed temporary download token** (`auth.MintDownloadToken`): the static route implements `auth.PublicReadAuthorizer` so a token whose signature matches the exact `(project, v, os, arch, fmt, debug)` tuple authorizes that one artifact under a private project (the rest stays gated), mirroring public-sites-under-private-projects. `token` is a known/canonical query param (so it is **not** stripped by the canonicalization redirect like other unknown params), and a token-bearing response is served `Cache-Control: private, no-store` so a shared CDN never caches private content. `SignedURL` builds the canonical static URL + token in one call.
- `internal/dl/` - Download handler on `dl.{domain}/{project}` with version/branch resolution via query params. Redirects to static. Self-registering via auth.OnReady().
- `internal/apt/` - APT repository endpoint on `apt.{domain}/{project}/...`. Pool downloads redirect to static. Self-registering via auth.OnReady().
- `internal/brew/` - Homebrew formula and generated tap endpoints. `brew.{domain}/tap.git` redirects to the cloneable Git tap at `git.{domain}/brew/tap.git`; formulas are also fetchable at `brew.{domain}/Formula/{project}.rb` and the legacy `brew.{domain}/{project}`. Formula download URLs point to tar.gz artifacts and sha256 values are computed from the same tar.gz payload Homebrew downloads. Self-registering via auth.OnReady().
- `internal/npm/` - npm registry endpoint on `npm.{domain}/@buildhost/{project}`. Tarball URLs point to static. Self-registering via auth.OnReady(). A pre-built `kind=npm-package` artifact's packument **reflects the uploaded tarball's own `package.json` manifest** (`npmManifestFields` reads `package/package.json` from the stored tarball and surfaces `dependencies`, `optionalDependencies`, `peerDependencies`/`peerDependenciesMeta`, `bundle(d)Dependencies`, `bin`, `os`, `cpu`, `engines`) rather than emitting a bare `{name,version,dist}` stub. Without this a package whose runtime depends on those fields -- e.g. a launcher whose `optionalDependencies` are its per-platform binary packages -- would publish with an empty dependency graph and install as an inert no-op (npm resolves against the packument, not the tarball). `name`/`version`/`dist` stay buildhost-authoritative and lifecycle `scripts` are deliberately never surfaced (serving a packument must not imply running install hooks); unreadable/blobs fall back to the minimal entry. `redirect.go` also registers a main-domain (host-agnostic) route that 301-redirects the apex `/npm/*` to `npm.{domain}/*` (prefix stripped), analogous to the `docker.{domain}` -> `oci.{domain}` redirect, so clients pointing an npm registry base at `https://{apex}/npm/` (e.g. the go-toolchain action) reach the npm subdomain. The redirect preserves the percent-encoded scope slash (`%2f`) via `r.URL.EscapedPath()` + string concatenation so the npm `GET /{pkg}` single-segment match still works.
- `internal/oci/` - OCI distribution endpoint (read + write) on `oci.{domain}/v2/{project}/...`. `docker.{domain}` permanently redirects to `oci.{domain}`. GET/HEAD pulls; POST/PATCH/PUT pushes (`docker push`). The base endpoint `GET/HEAD /v2/` performs OCI auth discovery: it answers `401` with `WWW-Authenticate: Basic realm="buildhost"` when unauthenticated and `200` only once a valid credential is in the request context (the global auth middleware verifies it) -- a `200` here would make clients conclude no auth is needed and never send credentials, killing the pull on the first manifest `401`. Pull side synthesizes a minimal image from a binary artifact (in `internal/repackage/oci.go`) OR serves a real pushed image. The synthesized image has **two layers**: a shared, deterministic, memoized "essentials" base layer (an embedded public CA bundle at `/etc/ssl/certs/ca-certificates.crt` so outbound TLS works, plus `/etc/passwd`+`/etc/group` with root/nobody/nonroot, `/etc/nsswitch.conf`, and a sticky `/tmp`) followed by the per-binary layer; the base layer is content-addressed (deduped to one blob server-wide) and registered per-pull as an `oci-base-layer` packaged artifact so the `BlobBelongsToProject` gate serves it. Each per-platform **image manifest is likewise persisted and linked per-pull** (`repackage.OCI.Repackage` stores it and `LinkOCIBlob`s it into `oci_blob_links`), so a multi-arch image index -- which lists each platform's manifest by digest -- has every child retrievable by `GET /v2/{project}/manifests/<digest>` (and `/blobs/<digest>`); `serveIndex` advertises only children that resolve, so it never emits a dangling index. `serveIndex` also persists the **top-level index document itself** under its own content digest (via `persistManifestBlob` -- the same `Store.Put`+`LinkOCIBlob` `PutManifest` applies to pushed manifests), so the synthesized index is retrievable by `GET/HEAD /v2/{project}/manifests/<index-digest>`, not only by tag. The Docker classic (non-containerd / overlay2) image store reads a manifest by tag, then re-fetches it by the advertised `Docker-Content-Digest` to store it content-addressably; without the persisted index that by-digest fetch 404'd and `docker pull <repo>:<tag>` failed with `manifest unknown` even though child platform pulls worked. Config sets `Env` (incl. `SSL_CERT_FILE`), `WorkingDir`, the `/<project>` entrypoint, and `User` from the release's optional `oci_user` field (empty = root). Push side (`push.go`, `upload.go`, `putmanifest.go`) accepts blob uploads (monolithic + chunked, streamed to `DataDir/tmp/oci-uploads`) and manifest/index PUTs, recording `kind=docker` artifacts. Route `Access()` is method-aware (write for push verbs). Self-registering via auth.OnReady().
- `internal/sites/` - Static site hosting endpoint on `sites.{domain}/{project}/...`. Upload tar.gz archives, serve files per branch or version. Self-registering via auth.OnReady(). A site uploaded with header `X-Public-Site: true` is stored with `is_public=1` and served **without a token even under a private project** -- the sites read route implements `auth.PublicReadAuthorizer` so the centralized `requireProject` opens just that one branch (the project's releases/other branches stay gated). Used for PR previews of private repos (the `buildhost-publish-site` action sends the header when `public: true`).
- `internal/llms/` - Public `/llms.txt` endpoint (https://llmstxt.org). Serves a plain-text guide to buildhost for LLMs/agents, rendered per request from an embedded `template.md` with the server's own base URL (derived from the request `Host`) substituted in. Registered on the apex (`HandleRaw`) **and on every service subdomain** (`ServiceHandleRaw` for each of apt/brew/dl/npm/oci/sites/static) -- the router's strict host partitioning means a known subdomain never falls through to the host-agnostic apex route, so without per-subdomain registration `oci.{domain}/llms.txt`, `npm.{domain}/llms.txt`, etc. would 404. `docker.{domain}/llms.txt` 301-redirects to `oci.{domain}` like every other docker path. The rendered guide always anchors its service URLs to the apex regardless of which host served it (`apexBaseURL` strips a known leading service label, mirroring the server's own first-label dispatch), so a request on `oci.{domain}` still renders `dl.{domain}` rather than `dl.oci.{domain}`. Public (no auth). Self-registering via init().
- `internal/web/` - Public, read-only browse frontend on the main domain (no subdomain). Server-rendered HTML via Go `html/template` (templates and the single `static/style.css` are embedded); **no JavaScript**, so the registry is indexable/consumable without a SPA. Routes: `GET /` (public project index, private projects filtered like `GET /api/v1/projects`), `GET /projects/{project}`, `GET /projects/{project}/releases/{version}`, and `GET /_ui/style.css`. Project/release pages register via `auth.Handle` with `auth.HiddenReadAccess`, so the shared `requireProject` middleware (the single home of project-auth) enforces visibility and returns a **`404`** -- never a `401` -- for a private project the viewer may not see, indistinguishable from a project that does not exist (GitHub-style; no existence leak). A read-scoped token authorized for the project reveals it. Only published releases are shown. Download links point at the `dl` subdomain (`dl.{host}/{project}?v=&os=&arch=&fmt=`); install snippets mirror llms.txt. The page handlers relax the global `default-src 'none'` CSP just enough for the one same-origin stylesheet (`style-src 'self'`); no `script-src` is ever emitted. Self-registering via init() (routes) + OnReady (DB). Distinct from `internal/admin/` (the authenticated admin SPA on a separate port).
- `internal/auth/` - Token auth, OIDC JWT verification, centralized project-auth middleware (requireProject), route registry (Handle/HandleRaw/HandleHandler for main-domain routes; ServiceHandle/ServiceHandleRaw/ServiceHandleHandler/ServiceRedirect for subdomain routes) backed by `github.com/wow-look-at-my/router`, RouteInfo interface. Service registrations are rewritten to host+path patterns (`<sub>.{domain}/<path>`) on the single router, so dispatch and route listing use the router's own host matching -- there is no per-subdomain dispatch table. `downloadtoken.go` mints/verifies **stateless, artifact-bound, expiring download tokens** (HMAC-SHA256 over `(project, version, os, arch, fmt, debug, expiry)` with a `bhdl_` prefix; key persisted at `{DataDir}/download-signing.key`, generated on first start like the APT key) used by the temporary-link endpoints, plus `ApexServiceURL` (derives the registry apex from any request Host by stripping a known leading service/admin label -- correct from the apex API too, unlike the unconditional-strip `DeriveServiceURL`).
- `internal/db/` - SQLite database layer (modernc.org/sqlite, no CGo), OIDC policy storage. Types (Project, Release, Artifact, APIToken, OIDCPolicy) and validation functions live here. Uses sqlc for query generation from `internal/db/queries/*.sql` with schema in `internal/db/schema.sql`.
- `internal/db/queries/` - SQL query files for sqlc code generation
- `internal/db/schema.sql` - SQLite schema used by sqlc
- `internal/storage/` - Content-addressed blob storage (filesystem backend, zstd-compressed, key validation). `Get` **memory-maps the compressed blob** (via `github.com/wow-look-at-my/go-mmap`, mapping the `os.Root`-opened fd to keep the path-traversal sandbox) and returns a streaming zstd decoder reading off the mapping, so a read never loads the whole artifact into the heap -- the decoder pulls kernel-paged pages on demand (`MADV_SEQUENTIAL`) and Close unmaps. Uncompressed blobs are served straight from the mapping; an empty blob returns an empty reader.
- `internal/repackage/` - On-demand repackaging and stripping (tar.gz, tar.xz, tar.zst, zip, deb, brew, npm, oci). **Everything streams**: `Input` carries an `io.Reader`+`Size` (not a `[]byte`) and each format pipes the artifact through its compressor via `io.Pipe` rather than buffering it, so memory is bounded by the compressor window, not the artifact size. `Output.Reader` is an `io.ReadCloser`; a streamed format whose compressed length isn't known up front returns `Size = SizeUnknown` (the handler then omits Content-Length and the body is chunk-encoded). `OpenArtifactStream` binds the (optionally stripped) reader to its exact size -- so a tar/ar/npm header can never disagree with the body -- and `ChainClose` ties the input stream's lifetime to the output reader (a lazily-read pipe keeps its source open until the consumer is done). deb spools only the *compressed* data.tar.gz member to a temp under `Input.TmpDir` to learn its `ar` length; oci streams the layer into `Store.Put` while teeing the uncompressed tar through sha256 for the diffID. Self-registering via init(); Generator uses registry. Orchestrator just publishes releases. `cacerts/ca-certificates.crt` is a public CA bundle baked into the synthesized OCI essentials layer via `//go:embed`. It is **fetched at build time** (`scripts/fetch-cacerts.sh`, run by the CI `build` job before go-toolchain) and **gitignored**, not committed -- so the repo carries no unreviewable cert blob. `go build` fails on a missing-embed error until the script has been run; see `cacerts/README.md`.
- `internal/strip/` - Binary debug info stripping (shells out to strip/objcopy). `StripReader`/`StripReaderDebug` spool a reader to a temp, run the file-based strip, and stream the stripped/debug file back (no `[]byte` round-trip). `strip` is a random-access ELF tool, so it inherently needs a file -- but it is **absent in the distroless prod image** (`Available()` is false), so the strip path never runs where the OOM happens.
- `internal/retention/` - Eviction policy + reference-counted garbage collection. Keeps the latest `BUILDHOST_RETENTION_KEEP_N` published releases per `(project, git_branch)` and sweeps abandoned (unpublished) uploads, then deletes content-addressed blobs no longer referenced by anything (the global `db.IsBlobReferenced`, generalizing `BlobBelongsToProject`). The whole eviction runs in one `db.EvictReleases` transaction that is **rolled back for dry-run / committed for enforce**, so report-only and enforce produce identical exact results (and a blob shared by several evicted releases is freed once). Single source of truth for the background sweeper (`cmd/buildhost/serve.go`), the `buildhost gc` CLI, and the **admin dashboard Retention page**. The policy (keep-N, recency guard) is **DB-backed and UI-editable** -- stored in the single-row `retention_settings` table, seeded from the `BUILDHOST_RETENTION_*` env defaults on first start (`SeedRetentionSettings`, INSERT OR IGNORE) and managed thereafter from the dashboard; the sweeper and CLI read it live each run via `db.GetRetentionSettings` + `retention.ConfigFromSettings`. The admin endpoints (`internal/admin/retention.go`: `GET/PUT /api/retention`, `POST /api/retention/run`) expose the policy, a dry-run preview (`Plan`), and on-demand enforce. `keep_n=0` still keeps each branch tip (the keep-N query floors at `max(keep_n, 1)`). **Report-only by default**: the background sweeper deletes only when `BUILDHOST_RETENTION_ENFORCE=true`; a manual dashboard/CLI run deletes when the operator confirms `--enforce`/the run button. Pins (never evicted): each branch's latest published release, oci-tagged releases, `kind=docker` releases, and anything newer than the recency guard. The shared `DeleteBlobIfUnreferenced` helper also fixes the sites delete/re-upload paths, which previously `Store.Delete`d unconditionally and could break a dedup-shared blob. Background sweeper is opt-in via `BUILDHOST_RETENTION_INTERVAL` (0 = off) and defers while writes are in flight (`admin.InflightWrites()`). NOTE: there is no standalone repackage-cache eviction -- non-OCI formats are regenerated per request, not stored (see `docs/eviction-policies.md`); dedicated docker/OCI blob GC is deferred.
- `internal/version/` - Release version resolution logic (resolves a version spec like `latest`/`v1`/semver to a `db.Release`)
- `internal/buildinfo/` - Build-time VCS stamps (commit, time, dirty flag) read once from `runtime/debug.ReadBuildInfo()`; reported by `buildhost version` and the `GET /healthz` JSON body
- `internal/admin/` - Admin dashboard (separate HTTP server, JSON API + static SPA frontend), inflight write counter for update coordination
- `internal/config/` - Server configuration from env vars
- `migrations/` - SQLite schema (embedded via go:embed)

## Key design decisions

- Versioning: auto-increment (default) or semver (opt-in per project)
- Git branch is a first-class field on releases, not just metadata
- Apex `latest` (a download with no `?v=` and no `?branch=` -- e.g. `dl/{project}/latest/{os}/{arch}`) resolves to the newest published release on **master**, the assumed default branch (`defaultBranch` in `internal/db/releases.go`), so a push to a feature branch never hijacks `latest`. When master has no published release yet, it falls back to the newest release across all branches. Centralized in `db.GetLatestRelease`, so dl/brew/apt/web, the OCI `latest` tag, and the npm `latest` dist-tag stay consistent. Per-branch downloads (`?branch=`) are unaffected.
- Repackaging and stripping happen on-demand at download time, not at publish time. Only the original upload is stored.
- **Bounded memory**: blob reads are mmap-backed and decoded as a stream, and every download/repackage path streams (no `io.ReadAll` of an artifact, no whole-archive `bytes.Buffer`), so per-request memory is bounded by the compressor window rather than the artifact size. The server also sets `GOMEMLIMIT` from the container's memory cgroup at startup (`automemlimit`, 0.9 ratio; no-ops if `GOMEMLIMIT`/`AUTOMEMLIMIT=off` is set), so the GC runs harder near the limit instead of letting the heap grow into an OOM-kill. Together these let buildhost serve artifacts far larger than its `mem_limit`.
- All artifact downloads go through `static.{domain}/file?project=&v=&os=&arch=&fmt=` -- a single CDN-cacheable endpoint with sorted query params, strong ETags, and immutable cache headers. Format handlers (dl, apt, brew, npm) redirect to static after resolving version/branch. `v=latest` returns 400 (callers must resolve first). Repackage formats self-register via `Fmt` interface.
- Storage is content-addressed (SHA-256) with zstd compression and deduplication
- Auth: Bearer token, Basic auth, or query param — all resolve to the same token system
- OIDC: JWT-based auth for GitHub Actions (and any OIDC provider), keys fetched from issuer's JWKS endpoint
- OIDC auto-provisioning: trusted issuers (BUILDHOST_OIDC_ISSUERS) can create projects on first publish -- project name derived from JWT subject claim; a repo's token is authorized for its own project and any `<repo>/<...>` sub-namespace (multi-binary repos publish each binary to `<repo>/<binary>`, e.g. `log-streamer/client`); org allowlist (BUILDHOST_OIDC_ORGS, matched case-insensitively, use `*` to allow all), event allowlist (BUILDHOST_OIDC_EVENTS, defaults to `push,pull_request` -- both limited to repo members since fork PRs get no OIDC token)
- Project names are slash-namespaced and may nest to any depth (`<repo>/<binary>`, even deeper). The api-layer validator (`internal/api/projects.go`, generated regex) allows `/`-separated segments; the `wow-look-at-my/router` `{project}` token matches multiple path segments greedily (anchored by trailing literals like `releases`/`artifacts`), so no `%2F` encoding is needed; storage is content-addressed so a `/` in a name never touches a filesystem path
- Private projects require auth on all endpoints including format-specific ones (APT, Brew, NPM, OCI)
- Project auth enforced once in centralized requireProject middleware — handlers never check auth
- Each backend defines a RouteInfo implementation (private route struct) for full URL parsing
- Backends self-register routes via auth.OnReady() on auth.Router(); adding a backend = adding files, no existing files modified. Each backend uses auth.ServiceHandle/ServiceHandleRaw/ServiceHandleHandler(subdomain, pattern, ...) for host-based routing; the registry prefixes the subdomain and a `{domain}` host token to the pattern so the router matches by Host (e.g. `apt.{domain}/{path...}`).
- Tokens are project-scoped or global; project-scoped tokens cannot escalate privileges
- Token expiry is enforced at lookup time
- Default token scope is "read" (least privilege). Scopes are `read`, `write`, and `share` (`db.ValidScopes`); `share` is a distinct permission to mint temporary download links (below), deliberately not implied by `write` so a CI/deploy token cannot hand out shareable links. The bootstrap admin token holds `read,write,share`.
- Temporary download links: a private artifact can be shared without a project token via a short-lived, **artifact-bound, HMAC-signed** URL -- `static.{domain}/file?...&token=bhdl_...`. Mint with `POST /api/v1/projects/{project}/download-links` (REST, requires a `share`-scoped token authorized for the project) or `POST /api/projects/{name}/download-links` (admin dashboard, trusted behind its reverse proxy). On a **private** project's release page the admin SPA's per-artifact `raw`/`debug`/`fmt` links would 401 through `dl`, so they mint-on-click instead (fetch a signed link, then download it); a plain "temp link" button copies a shareable signed URL. Public projects keep the plain (cacheable, permanent) `dl` links. The signature binds `(project, version, os, arch, fmt, debug)` + expiry (default 1h, max 24h), so a leaked link exposes only that one file until it expires; it is stateless (no DB row, not individually revocable -- rotate `download-signing.key` to invalidate all). The link points at `static` directly (no `dl` hop) with the version already resolved, since the binding needs an exact version.
- Upload size capped at 2 GiB (REST artifact PUT, configurable via `BUILDHOST_MAX_UPLOAD_SIZE`); JSON endpoints capped at 1 MiB. OCI `docker push` uploads each layer as a separate blob with its own cap (`BUILDHOST_MAX_BLOB_SIZE`, default 10 GiB), so multi-GB images are not bound by the REST cap
- Docker push: a release containing pushed `kind=docker` artifacts is a "docker build" -- served only via the OCI endpoint. `kind=docker` is gated out of apt/brew/npm and the raw `/static` (+ `/dl`) paths. Pushed blobs/manifests are linked to the project in `oci_blob_links` (so the existing `BlobBelongsToProject` pull gate serves them); pushed tags live in `oci_tags` as mutable pointers (`latest` is an alias, digests are immutable, identical re-push is a no-op, a changed image creates a new auto-versioned release and repoints the tag). `docker login` uses Basic auth -> the token system; a GHA OIDC JWT works as the password and auto-provisions the project
- Storage keys validated as hex SHA-256 to prevent path traversal
- Static sites: uploaded as tar.gz (`Content-Type: application/gzip`) or zip (`Content-Type: application/zip`). Both formats are stored as raw tar internally and served by scanning tar headers per request. Each branch is an independent deployment (one row in `sites` table). Re-deploying a branch replaces the previous site atomically. Upload size capped at 256 MiB, max 10,000 files per site. A site row carries a per-branch `is_public` flag (set by the `X-Public-Site: true` upload header): a public site is served anonymously even when the project is private, so a PR preview of a private repo gets a working shareable URL without exposing the project's release artifacts. The project's own visibility is unchanged (still derived from the OIDC `repository_visibility` claim and re-synced on every write).

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

## Running

```bash
BUILDHOST_LISTEN_ADDR=:8080 buildhost serve
```

Each service is accessed via a subdomain derived from the incoming request's `Host` header: `apt.example.com`, `brew.example.com`, `git.example.com`, `npm.example.com`, `oci.example.com` (canonical, `docker.example.com` 301-redirects), `dl.example.com`, `sites.example.com`, `static.example.com`. API routes stay on the main domain. No domain configuration is required -- the server dispatches by matching the first label of the Host header against known service names.

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

`GET /ready-to-update` on the main server (`:8080`) returns HTTP 200 when the server is idle, or HTTP 503 when there are in-flight write requests. It is designed for docker-updater's HTTP pre-update checks -- no exec into the container (the distroless image has no shell).

docker-updater reaches it over the **shared `internal` Docker network** via buildhost's DNS alias, configured as a full URL:

```yaml
labels:
  docker-updater.pre-check.url: "http://buildhost-backend:8080/ready-to-update"
```

Do **not** use docker-updater's `:8080/...` (port-only) pre-check form: that one resolves the container's bridge IP and therefore requires running docker-updater with `--network host`. The full-URL form only needs docker-updater to share a network with buildhost (it joins `internal` in `deploy/docker-compose.yml`), which keeps the updater off host networking. Note that rolling updates (below) **skip** pre-checks entirely -- the old container drains via graceful shutdown -- so the pre-check endpoint matters only for non-rolling setups and the `try-update` CLI.

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

`internal/server/llms_endpoints_test.go` guards the `/llms.txt` document against drift: it parses the *served* document and asserts every URL it references resolves to a registered route, then exercises the documented flows (downloads, APT/Brew/npm/OCI, the `/static` latest-rejection) end to end against a seeded server. Editing `internal/llms/template.md` to reference a nonexistent endpoint fails CI.

`test/e2e/` is a synthesized-OCI-image end-to-end test (CI job `synthesized-image-e2e` in `ci.yml`, not part of `go-toolchain`). It publishes a tiny static binary (`test/e2e/testdata/netcheck/`; under `testdata/` so `go list ./...` -- and thus go-toolchain's build/vet/coverage -- ignores it, while the e2e job still builds it explicitly) to a real `buildhost serve`, then uses **crane** (go-containerregistry) to pull the image buildhost synthesizes, assert its config (entrypoint, `SSL_CERT_FILE`, two ordered `diff_ids`) and flattened rootfs (CA bundle, `nonroot` in `/etc/passwd`, sticky `/tmp`), and run the entrypoint -- which does an outbound HTTPS request validated **only** against the image's baked-in CA bundle, proving a networked service works in the synthesized image. crane (not docker) because buildhost's layers are `tar+zstd`, which go-containerregistry pulls but the default GitHub Docker may not.

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
- **Token in query param**: Intentional for clients that cannot set headers (APT, Brew). Mitigated by Referrer-Policy: no-referrer and redaction from OTEL trace attributes
- **Temporary download links**: `&token=bhdl_...` is a stateless HMAC-SHA256 signature over `(project, version, os, arch, fmt, debug, expiry)` keyed by `{DataDir}/download-signing.key` (32 random bytes, 0600, generated on first start). It only ever *grants* read to the single artifact it is signed for under an otherwise-private project -- it cannot escalate, cross projects, or outlive its (capped, <=24h) expiry, and verification is constant-time (`hmac.Equal`). Minting requires the `share` scope (REST) or the access-controlled admin dashboard. Trade-off: links are not individually revocable before expiry (rotate the key to invalidate all); acceptable for short-lived links. Token-gated responses are `Cache-Control: private, no-store` so the shared CDN never caches them. Same query-param exposure profile as the existing APT/Brew token param (Referrer-Policy: no-referrer, OTEL redaction), but short-lived and single-artifact rather than a full project token.
- **No TLS termination**: Intentional -- runs behind a reverse proxy in Docker
- **Strip temp file permissions**: Runs in a single-user Docker container; permissions are 0600 anyway
- **APT Release signing**: RSA 4096 key auto-generated on first startup, stored in `BUILDHOST_DATA_DIR/apt-signing.key`. InRelease (clearsigned), Release.gpg (detached), and key.asc (public key) endpoints are all served. Clients add the key via `curl .../key.asc | gpg --dearmor > /etc/apt/keyrings/buildhost.gpg` and use `[signed-by=/etc/apt/keyrings/buildhost.gpg]` in sources.list
- **List endpoints**: No LIMIT -- all behind auth, SQLite serialized, not a DoS vector
- **Symlink rejection**: Storage layer rejects symlinks via Lstat check
- **Admin dashboard auth**: None -- must be behind a reverse proxy with access control (Cloudflare Access, etc.)
- **Container user**: Runs as nonroot (UID 65532) via distroless base image
- **Graceful shutdown**: Server handles SIGTERM/SIGINT with 5-minute timeout for clean connection draining
- **Ready-to-update endpoint**: `GET /ready-to-update` on :8080 returns 200/503 with no body content -- reveals only idle/busy state, no sensitive data
- **Inflight endpoint**: `GET /admin/inflight` on :9090 is unauthenticated -- same trust model as the rest of the admin dashboard (internal-only, behind reverse proxy)
- **No writes outside data dir**: Temp files use BUILDHOST_DATA_DIR/tmp, not system /tmp
- **OIDC audience check**: The auto-provisioning path does NOT gate on the token's `aud` claim -- trust for a trusted-issuer token comes from the JWKS signature plus the org allowlist (`BUILDHOST_OIDC_ORGS`), the event allowlist (`BUILDHOST_OIDC_EVENTS`), and the subject claim. (Telling the server its own URL was never a meaningful trust boundary, and a stale/missing value caused a production 401 outage, so the gate was removed.) A per-policy `audience` field on an `OIDCPolicy` is still honored as an optional, opt-in restriction for explicitly configured policies. The server is never told its own URL: generated links are derived per request from the `Host` header (`auth.RequestBaseURL`).
- **OIDC event check**: Tokens without an `event_name` claim are rejected when `BUILDHOST_OIDC_EVENTS` is configured (default: `push,pull_request`). This prevents bypass via providers that omit the claim. Fork PRs in GitHub Actions do not receive OIDC tokens, so `pull_request` is safe to include by default.
- **OIDC RSA key size**: JWKS keys below 2048 bits are rejected
- **OIDC visibility sync**: When an OIDC token's `repository_visibility` claim changes project visibility, the change is logged at WARN level with project name, old/new visibility, and OIDC subject
- **Sites decompression**: Decompressed tar size is capped at 1 GiB to prevent gzip bomb attacks. ZIP uploads are also bounded by the 256 MiB upload limit and the 1 GiB decompressed tar cap.
- **Sites security headers**: Served site files drop the app's strict `Content-Security-Policy: default-src 'none'` and `X-Frame-Options: DENY` that the global middleware sets (`internal/sites/serve.go` `setSiteSecurityHeaders`). Hosted sites are third-party static content on the dedicated `sites.{domain}` subdomain (isolated from the app/admin origins) and must be able to load their own and external assets, like any static host. They set `Access-Control-Allow-Origin: *` for non-credentialed cross-origin asset reads, plus `Cross-Origin-Opener-Policy: same-origin` and `Cross-Origin-Embedder-Policy: credentialless`. `X-Content-Type-Options: nosniff` is kept.
- **Public sites under private projects**: A site uploaded with `X-Public-Site: true` is served anonymously even when its project is private. This is **opt-in per upload** (the publisher's own OIDC/token write explicitly sets it) and **scoped to that one site branch** -- it does not change the project's visibility, and release artifacts / other branches / the `/branches` listing stay gated. The bypass is enforced centrally in `requireProject` (the sites read route implements `PublicReadAuthorizer`), not by the sites handler, so the "auth enforced once" invariant holds. The `buildhost-publish-site` action defaults `public` to `false`; the PR-preview reusable workflow opts in (`public: true`) because preview URLs are meant to be shareable -- matching how a private repo can already serve a public GitHub Pages site.
- **Admin error messages**: Admin API handlers return generic error messages; raw errors are logged server-side only
- **Migrations**: Each migration runs in a single transaction (DDL + schema_migrations record) to prevent partial application on crash
- **OIDC auto-provisioning**: Trusted issuers can auto-create projects. Project name derived from subject claim (repo:org/name:* -> name), lowercased and validated against `[a-z0-9][a-z0-9._-]{0,127}`. Authorized for read,write on that repo's whole namespace -- project `R` plus any slash-namespaced `R/<...>` beneath it (so a multi-binary repo publishes `R/<binary>` for each binary), gated by a trailing-slash boundary so sibling prefixes (`R-evil`) and unrelated projects are refused. `requireProject` validates an auto-created namespaced name per-segment before creating it. Optional BUILDHOST_OIDC_ORGS allowlist restricts which orgs can auto-provision. **Provisioning is write-only**: `requireProject` only creates a missing project for a `WriteAccess` route (the publish POST/PUT flow, docker push, site deploy). A read (`ReadAccess`/`HiddenReadAccess`: dl/static/apt/brew/npm and the web frontend) never provisions -- a GET 404s instead, so it can never materialize a project as a side effect.
- **OIDC_ORGS wildcard risk**: Setting `BUILDHOST_OIDC_ORGS=*` allows any GitHub org to auto-provision projects. Since project names are derived from repo names, any repo in any org with the same name as an existing project would derive the same project name. The first push creates the project; subsequent pushes from other orgs are blocked by `AuthorizedForProjectName`. However, avoid `BUILDHOST_OIDC_ORGS=*` in production -- scope the allowlist to trusted orgs only
