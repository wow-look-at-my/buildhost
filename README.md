# buildhost

Self-hosted universal package registry. Upload a release artifact once, download it in any packaging format.

## Supported formats

From a single uploaded binary, buildhost serves:

- **Raw binary** download
- **tar.gz**, **tar.xz**, **tar.zst** archives
- **zip** archive
- **APT repository** (`.deb` packages with repo metadata)
- **Homebrew tap** (Ruby formula with computed sha256)
- **npm registry** (platform-specific npm packages)
- **OCI/Docker registry** (minimal container images synthesized from the binary, with CA certificates and a minimal rootfs so networked services run out of the box)

## Homebrew

[docs/homebrew.md](docs/homebrew.md) holds this section.

## APT (Debian / Ubuntu)

[docs/apt-debian-ubuntu.md](docs/apt-debian-ubuntu.md) holds this section.

## Web frontend

buildhost serves a public, read-only browse UI on the main domain, and it uses no subdomain. It is plain server-rendered HTML with **no JavaScript**. A crawler or an agent can therefore consume and index it, with no single-page app to evaluate.

- `GET /` -- an index of every public project
- `GET /projects/{project}` -- a project's metadata, its published releases, its deployed static sites, and copy-paste install and download commands
- `GET /projects/{project}/releases/{version}` -- a release's artifacts, with a download link per format. The formats are `raw`, `tar.gz`, `tar.xz`, `tar.zst` and `zip`. An image release gets a `docker pull` instead.

A private project is hidden. An anonymous visitor never sees it in a listing. A direct visit to its page returns a `404`, identical to the answer for a project that does not exist. The frontend therefore never reveals that a private project exists, the same way GitHub treats a private repository. A read-scoped token authorized for the project reveals it.

A download link points at the `dl` subdomain. The single stylesheet is served from `/_ui/style.css`. No other asset is loaded. The authenticated admin dashboard remains a separate app on its own port. See [Container image](#container-image).

## Synthesized container images

A project can hold only a plain binary, with no pushed image. The registry then synthesizes an OCI image from it on demand, so `docker pull` and `crane pull` both work. The image is deliberately minimal. It still ships the runtime essentials of `gcr.io/distroless/static`. A networked service therefore works with no real image pushed.

- A real public **CA certificate bundle** at `/etc/ssl/certs/ca-certificates.crt` (and `SSL_CERT_FILE` pointing at it), so outbound HTTPS works -- no more `x509: certificate signed by unknown authority`.
- `/etc/passwd` and `/etc/group` with `root`, `nobody` and `nonroot` (UID 65532), an `/etc/nsswitch.conf` (`hosts: files dns`) and a sticky `/tmp`.
- The binary at `/<project>` as the entrypoint, a sane `PATH`, and `WorkingDir=/`.

The image runs as **root** by default. To run it as another user, set `oci_user` on the release. That field takes `uid[:gid]` or `name[:group]`, such as `65532:65532` for the bundled nonroot user. It is emitted as the image's `config.User`.

```bash
buildhost publish --oci-user 65532:65532 ...   # or oci_user in a release manifest / the
                                               # oci_user field of the create-release JSON
```

The synthesized image is regenerated on demand, and nothing stores it. Its digest is therefore not pinned. It can change between buildhost versions.

## Publishing real Docker images

[docs/publishing-real-docker-images.md](docs/publishing-real-docker-images.md) holds this section.

## GitHub Deployments

The top-level publish actions are `buildhost-publish`, `buildhost-publish-site` and `buildhost-publish-docker`. Each one registers its publish as a GitHub Deployment in the calling repo. The deployment appears in that repo's Environments and Deployments UI, with a "View deployment" link to the live buildhost URL. That URL is the release page, the site URL, or the project page.

- It is on by default, through `create_deployment: 'true'`. It needs `deployments: write` in the calling job. Without that grant the step warns, and the publish proceeds.
- `deployments: write` is **additive** to each action's existing permissions, so keep `id-token: write` and the rest. A job-level `permissions:` block replaces the workflow-level one. List the full set wherever you declare one.
- An environment auto-names as `buildhost/<project>`. A site auto-names as `buildhost/<project>/<branch>`. `deployment_environment` overrides both.
- Set `create_deployment: 'false'` to opt out. That also silences the warning.

## Container image

A container image is published to `ghcr.io/wow-look-at-my/buildhost:latest` on every push to master.

The image is based on `gcr.io/distroless/static-debian12:nonroot` and runs as UID 65532. It contains:

- `/usr/local/bin/buildhost` -- a `#!/bin/sh` launcher, the entrypoint
- `/usr/local/lib/buildhost/buildhost` -- the binary the launcher starts
- `/bin` -- one static busybox, which the binary needs to start
- CA certificates (from distroless base)
- `/etc/passwd` with `nonroot` user (UID 65532)

No package manager and no other binaries. Why the launcher exists, and why the entrypoint must never name the binary directly: `docs/deploy-and-updates.md`.

### Recommended docker-compose configuration

```yaml
services:
  buildhost:
    image: ghcr.io/wow-look-at-my/buildhost:latest
    ports:
      - "8080:8080"
    volumes:
      - buildhost-data:/var/lib/buildhost
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    read_only: true
    pids_limit: 256
    mem_limit: 512m
    networks:
      - buildhost

  # The admin dashboard (port 9090) has NO built-in authentication.
  # It MUST be placed behind a reverse proxy with access control
  # (e.g., Cloudflare Access on a separate hostname).
  # Do NOT expose port 9090 to untrusted networks.

networks:
  buildhost:
    driver: bridge
    internal: true

volumes:
  buildhost-data:
```

**Note:** the server reads the container's memory cgroup at startup. It sets `GOMEMLIMIT` to about 90% of that, through [automemlimit](https://github.com/KimMachineGun/automemlimit). Every download and repackage path streams. A blob read is mmap-backed, and nothing buffers a whole artifact. buildhost therefore serves an artifact far larger than `mem_limit` without an OOM. Set `GOMEMLIMIT` yourself, or `AUTOMEMLIMIT=off`, to override that.

**Note:** an ELF binary is stripped on download. Its symbols are served separately, at `?fmt=symbols`. `?debug=1` returns the artifact exactly as uploaded. Stripping runs in-process. It therefore needs no `strip` and no `objcopy` in the image. Anything that is not an ELF is always served byte-for-byte as uploaded. A Cosmopolitan APE, a Mach-O and a script are all in that group.

**Note:** service through Cloudflare's proxy caps a request body at the edge, at 100 MB on the Free plan. [`deploy/DIRECT-INGRESS.md`](deploy/DIRECT-INGRESS.md) adds an opt-in direct TLS ingress. An upload of any size then works in a single request.

## Draft releases

A release you upload but do not publish is a **draft**. It is downloadable by its exact version, and nothing else sees it. `latest`, a per-branch download, Homebrew, APT, npm and OCI all resolve a published release only. Use a draft to put a build somewhere you can `curl` it, without a move of the pointer everyone follows.

```bash
buildhost publish --draft --server https://buildhost.example.com --token $TOKEN \
  --project myapp --os linux --arch amd64 --artifact ./myapp
# draft myapp/7 (not published)
# download: https://static.buildhost.example.com/file?project=myapp&v=7&os=linux&arch=amd64
```

Publish it later (`POST /api/v1/projects/{project}/releases/{version}/publish`) and it joins the release stream, clearing the draft flag. Drafts are kept until you delete them: retention sweeps *unpublished* releases as abandoned uploads, but never drafts.

## Quick start

```bash
# Start the server
buildhost serve

# Create a token (first time setup)
buildhost token create --server http://localhost:8080 --token $BOOTSTRAP_TOKEN --name ci

# Create a project
buildhost project create --server http://localhost:8080 --token $TOKEN --name myapp

# Publish an artifact
buildhost publish \
  --server http://localhost:8080 \
  --token $TOKEN \
  --project myapp \
  --os linux --arch amd64 \
  --artifact ./myapp-linux-amd64

# Download
curl -O http://localhost:8080/dl/myapp/latest/linux/amd64
```

## Multi-platform binaries (Cosmopolitan / APE)

[docs/multi-platform-binaries-cosmopolitan-ape.md](docs/multi-platform-binaries-cosmopolitan-ape.md) holds this section.

## WebAssembly artifacts

A WebAssembly module publishes under the platform identifier `os=wasm`. `arch` names the flavor. `js` is Go's browser and Node port, from `GOOS=js GOARCH=wasm`. `wasip1` is the WASI port, from `GOOS=wasip1 GOARCH=wasm`.

```bash
# Upload both Go wasm ports (one request each, or a comma list)
curl -X PUT -H "Authorization: Bearer $TOKEN" --data-binary @app.js.wasm \
  http://localhost:8080/api/v1/projects/myapp/releases/1/artifacts/wasm/js
curl -X PUT -H "Authorization: Bearer $TOKEN" --data-binary @app.wasip1.wasm \
  http://localhost:8080/api/v1/projects/myapp/releases/1/artifacts/wasm/wasip1

# Download
curl -LO "https://dl.example.com/myapp?os=wasm&arch=js"
```

For filename-derived uploads (`name_os_arch`), name the files `myapp_wasm_js` / `myapp_wasm_wasip1`.

`os=wasm` pairs only with the `js` and `wasip1` arches, and each of those pairs only with `wasm`. Any other combination is rejected with a 400, and `wasm/amd64` and `linux/js` are both examples.

The multi-platform aliases deliberately exclude wasm. `cosmo`, `any` and `all` mean "runs on every native desktop platform". A wasm module instead needs a JS host or a WASI runtime. Publish wasm explicitly.

A wasm artifact is served raw, and through the archive formats such as `tar.gz` and `zip`. It never appears in the APT index or in the Homebrew tap, which are linux and darwin only by construction.

**There is a deprecated legacy compatibility shim.** A currently-released go-toolchain autorelease derives its upload parameters from a GOOS_GOARCH-ordered filename, such as `name_js_wasm` or `name_wasip1_wasm`. It therefore uploads with `os=js` and `arch=wasm`, or with `os=wasip1` and `arch=wasm`.

That exact pair is folded to the canonical form at parse time. The fold runs on upload, on a `dl` query, and in the static endpoint's canonicalization redirect. Such an upload therefore succeeds. It is stored, listed and served as `os=wasm` with `arch=js` or `arch=wasip1`. `js` is never stored or surfaced as an os anywhere.

The shim is pair-level only. `os=js` with any other arch stays invalid, and so does `arch=wasm` with any other os. It exists for an older go-toolchain release. A new publisher must use the canonical `os=wasm` form.

## Versioning

Projects use auto-incrementing versions by default (v1, v2, v3...). Opt into semver with `--versioning semver` at project creation. A semver release that names no version gets the patch after the latest one.

Git branch and commit are tracked on every release. Download the latest build of a branch:

```
GET /dl/myapp/branch/main/linux/amd64
```

`latest`, with no branch, resolves to the newest published release on the project's **default branch**. That branch is `master` by default. buildhost detects each repo's real default branch automatically. On a GitHub Actions OIDC publish it reads the `owner/repo` from the token, and asks GitHub for that repo's default branch. A repo that releases off another branch, such as `v1`, therefore gets a correct `latest` with nothing sent in the publish. A push to a feature branch never hijacks `latest`. When the default branch has no published release yet, `latest` is not available.

buildhost authenticates these lookups as a **GitHub App**. Set `BUILDHOST_GITHUB_APP_ID` and `BUILDHOST_GITHUB_APP_PRIVATE_KEY`, and the key takes the PEM contents or a file path. That path is recommended, because it mints a short-lived installation token, needs `metadata: read` only, and carries a high rate limit. A static `BUILDHOST_GITHUB_TOKEN` PAT works as a fallback. Without either, a lookup is anonymous. GitHub throttles an anonymous lookup to 60 per hour per IP. It cannot read a private repo.

## Static sites

Host small, self-contained static sites with independent per-branch deployments. Each branch gets its own site that exists from first deploy until explicitly deleted. Directory requests serve `index.html`. If a requested file is missing and the uploaded site contains a root `404.html`, buildhost serves that page with HTTP 404.

A site is served on the `sites.` subdomain, as every other service is. Pass the apex to `--server`, and the CLI derives that subdomain.

```bash
# Deploy a site from a directory
buildhost publish-site \
  --server http://localhost:8080 \
  --token $TOKEN \
  --project myapp \
  --branch main \
  --dir ./dist

# The site is available at its own root path, on the default branch:
# http://sites.localhost:8080/myapp/           (index.html)
# http://sites.localhost:8080/myapp/index.css  (any file)

# Any other branch, or a specific commit, is named with the @ sigil:
# http://sites.localhost:8080/myapp/@pr-7/index.css
# http://sites.localhost:8080/myapp/@0f1e2d3/index.css

# Redirects only run toward the shorter URL: @main (the default branch) 302s to
# /myapp/, and the original /myapp/branch/main/ spelling 302s to whichever of the
# two URLs above names the same file -- so every published link keeps resolving.

# Re-deploying the same branch replaces the previous site atomically.
# Deleting a branch deployment:
curl -X DELETE -H "Authorization: Bearer $TOKEN" \
  http://sites.localhost:8080/myapp/@main
```

### Project site subdomains (optional)

<<<<<<< HEAD
Set `BUILDHOST_SITE_DOMAIN` (e.g. `pazer.site`) to also serve each project's site at `https://<project>.<domain>/` -- the default branch on bare paths. This is any other branch (or commit) behind the `@` sigil: `https://myapp.pazer.site/@pr-7/`, `https://myapp.pazer.site/@0f1e2d3/`.
=======
Set `BUILDHOST_SITE_DOMAIN` (e.g. `pazer.site`) to also serve each project's site at `https://<project>.<domain>/`. Bare paths serve the default branch. Any other branch or commit sits behind the `@` sigil: `https://myapp.pazer.site/@pr-7/`, `https://myapp.pazer.site/@0f1e2d3/`.
>>>>>>> origin/master

- A slash-named branch, such as `claude/foo`, resolves by longest match. An `@<default-branch>` URL 302s to the canonical bare form. The `~` sigil this scheme launched with still works, and it 301s to the `@` form.
- Only a project name that is a single DNS label serves here. That means `[a-z0-9-]`, a bounded number of characters, with no leading or trailing `-`. Every other name stays on `sites.<apex>/...`.
- Reserved on this scheme: a leading `~` path segment and the literal `/__sso`.
- Private sites sign in via the primary apex: set `BUILDHOST_PRIMARY_DOMAIN` (e.g. `pazer.build`). The browser authenticates there (same single GitHub OAuth app), then is handed back to the site domain -- no second OAuth app.
- Setting `BUILDHOST_PRIMARY_DOMAIN` also scopes the web UI and `/api/v1` to that apex: other hosts get a plain 404 (health, sign-in, `llms.txt` stay host-agnostic). Unset, everything stays host-agnostic as before.

## Large uploads

[docs/large-uploads.md](docs/large-uploads.md) holds this section.

## Tokens

Tokens authenticate all API requests. There are kinds:

- **A global token** omits `project_id`. It can access every project. It can also manage tokens.
- **A project-scoped token** sets `project_id`. It is limited to one project. It cannot list or delete a token.

Each token has a `scopes` field. That field is a comma-separated subset of `read`, `write` and `share`. The default, when it is omitted, is `read`. A token can grant only a scope it already holds, so a read-only token cannot mint a write token.

`share` is a distinct permission. It mints a [temporary download link](#temporary-download-links). `write` does not imply it. A CI or deploy token therefore cannot hand out a shareable link to a private artifact. The bootstrap admin token holds `read,write,share`.

### First-time setup

On a fresh server with no tokens, use `buildhost bootstrap` to create the first admin token. It reads from the database directly and does not require a running server.

```bash
buildhost bootstrap                    # creates token named "admin"
buildhost bootstrap --name admin-token # custom name
```

The plaintext token is printed to stdout. Store it securely — it is not retrievable later.

### Create a token (CLI)

```bash
buildhost token create \
  --server https://buildhost.example.com \
  --token $ADMIN_TOKEN \
  --name ci \
  --scopes read,write
```

To create a project-scoped token, pass `--scopes` and include `project_id` in the request body directly via curl (the CLI does not expose `--project-id` yet — see below).

### Create a token (API)

```bash
# Global read+write token
curl -X POST https://buildhost.example.com/api/v1/tokens \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "ci", "scopes": "read,write"}'

# Project-scoped read token (project id 3)
curl -X POST https://buildhost.example.com/api/v1/tokens \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "deploy-bot", "project_id": 3, "scopes": "read,write"}'
```

Response:

```json
{
  "token": "bh_plaintext_value_shown_once",
  "details": { "id": 7, "name": "ci", "scopes": "read,write", ... }
}
```

### List and delete tokens

```bash
# List all tokens (global token required)
buildhost token list --server https://buildhost.example.com --token $ADMIN_TOKEN

# Delete by id (global token required; cannot delete your own token)
curl -X DELETE https://buildhost.example.com/api/v1/tokens/7 \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

### Using a token

All forms are equivalent:

```bash
# Bearer token (preferred)
curl -H "Authorization: Bearer $TOKEN" https://buildhost.example.com/api/v1/projects

# Basic auth (password field is the token; username is ignored)
curl -u "token:$TOKEN" https://buildhost.example.com/api/v1/projects

# Query parameter (for clients that cannot set headers, e.g. APT, Brew)
curl "https://buildhost.example.com/api/v1/projects?token=$TOKEN"
```

## Temporary download links

To share a single artifact from a **private** project without handing out a token, mint a temporary, signed download link. The link works for exactly one artifact (`os`/`arch`/`fmt`/`version`) and expires (default 1 hour, max many hours).

```bash
curl -X POST https://buildhost.example.com/api/v1/projects/myapp/download-links \
  -H "Authorization: Bearer $SHARE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"os": "linux", "arch": "amd64", "version": "3", "fmt": "raw", "ttl_seconds": 3600}'
```

```json
{
  "url": "https://static.example.com/file?arch=amd64&fmt=raw&os=linux&project=myapp&token=bhdl_...&v=3",
  "token": "bhdl_...",
  "expires_at": "2026-06-11T12:00:00Z"
}
```

Anyone with the `url` can download that artifact until it expires — no account or token needed. Minting requires a token with the `share` scope, authorized for the project. The admin dashboard exposes the same thing as a **"temp link"** button on each release's artifact list.

The link is a stateless HMAC signature. A server-side key, generated on the first start, keys it. The signature is bound to the exact artifact and expiry. A leaked link therefore reaches nothing else in the project. It cannot outlive its expiry. A link is not individually revocable before its expiry. Rotate the signing key to invalidate every outstanding link.

## API

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/tokens` | Create token |
| GET | `/api/v1/tokens` | List tokens (global token required) |
| DELETE | `/api/v1/tokens/{id}` | Delete token (global token required) |
| POST | `/api/v1/projects/{project}/download-links` | Mint a temporary signed download link (`share` scope) |
| POST | `/api/v1/projects` | Create project |
| GET | `/api/v1/projects` | List projects |
| POST | `/api/v1/projects/{project}/releases` | Create release |
| PUT | `/api/v1/projects/{project}/releases/{version}/artifacts/{os}/{arch}` | Upload artifact (accepts `?upload_session=` -- see [Large uploads](#large-uploads); `{os}`/`{arch}` may be a comma list or `cosmo`/`any`, and an empty body + `?upload_sha256=` registers an already-uploaded blob for the slot -- see [Multi-platform binaries](#multi-platform-binaries-cosmopolitan--ape)) |
| POST | `/api/v1/projects/{project}/releases/{version}/publish` | Publish release |
| GET | `/api/v1/server-info` | Advertised upload limits and capabilities (public) |
| POST | `/api/v1/uploads` | Create a chunked upload session |
| GET | `/api/v1/uploads/{id}` | Read a session's committed size |
| PATCH | `/api/v1/uploads/{id}?offset=N` | Append a chunk to a session |
| DELETE | `/api/v1/uploads/{id}` | Abort a session |
| POST | `/api/v1/webhooks/github` | GitHub org webhook receiver for branch deletion cleanup |
| GET | `/dl/{project}/{version}/{os}/{arch}` | Download |
| GET | `/dl/{project}/latest/{os}/{arch}` | Download latest |
| GET | `/dl/{project}/branch/{branch}/{os}/{arch}` | Download latest for branch |
| PUT | `/sites/{project}/@{branch}` | Deploy static site (tar.gz body) |
| DELETE | `/sites/{project}/@{branch}` | Remove static site |
| GET | `/sites/{project}/{path}` | Serve a file from the default branch (canonical) |
| GET | `/sites/{project}/@{ref}/{path}` | Serve it from a branch or commit |
| GET | `/sites/{project}/branch/{branch}/{path}` | 302 to the canonical URL above |
| GET | `/sites/{project}/branches` | List branch deployments |
| GET | `/llms.txt` | Plain-text guide to buildhost for LLMs ([llmstxt.org](https://llmstxt.org)) |
| GET | `/healthz` | Liveness check (database ping); JSON body reports the running build's commit and version |
| GET | `/` | Public read-only web frontend: index of public projects |
| GET | `/projects/{project}` | Web frontend: project page (releases, install commands) |
| GET | `/projects/{project}/releases/{version}` | Web frontend: release page (artifacts + download links) |

## llms.txt

`GET /llms.txt` serves a public, unauthenticated plain-text document that explains what buildhost is and how to use it, aimed at LLMs and automated agents. Example URLs in the document are rendered against the request's `Host`, so they always point at the live deployment.

## Health and version

`GET /healthz` returns `200` when the server is up and its database is reachable, and `503` when the database is unreachable. The JSON body reports the exact build the server runs, either way. You can therefore check which image a deployment is on.

```json
{"status":"ok","commit":"<git-sha>","version":"v0.0.<unix>"}
```

The same build info is printed by `buildhost version`.

## Configuration

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `BUILDHOST_LISTEN_ADDR` | `:8080` | API listen address |
| `BUILDHOST_ADMIN_LISTEN_ADDR` | `:9090` | Admin dashboard listen address (empty to disable) |
| `BUILDHOST_DATA_DIR` | `./data` | Data directory |
| `BUILDHOST_DB_PATH` | `./data/buildhost.db` | SQLite database path |
| `BUILDHOST_OIDC_ISSUERS` | (none) | Comma-separated trusted OIDC issuers for auto-provisioning |
| `BUILDHOST_OIDC_ORGS` | (none) | Comma-separated allowed orgs for OIDC auto-provisioning, matched case-insensitively (`*` for all) |
| `BUILDHOST_OIDC_EVENTS` | `push,pull_request,workflow_dispatch` | Comma-separated allowed event types for OIDC auto-provisioning (`*` for all) |
| `BUILDHOST_GITHUB_WEBHOOK_SECRET` | (off) | Enables `POST /api/v1/webhooks/github`; used to verify GitHub webhook HMAC signatures |
| `BUILDHOST_MAX_UPLOAD_SIZE` | `2G` | Cap on a single artifact's total size, direct or assembled from chunks |
| `BUILDHOST_MAX_DIRECT_UPLOAD_SIZE` | `95M` | Advertised safe single-request size (`/api/v1/server-info`); keep it under any proxy body cap in front of the server (Cloudflare's edge caps at 100 MB) |
| `BUILDHOST_UPLOAD_SESSION_TTL` | `24h` | How long an idle chunked upload session lives before its spool is swept |
| `BUILDHOST_RETENTION_INTERVAL` | (off) | Background GC sweep cadence (e.g. `1h`); empty/`0` disables the sweeper |
| `BUILDHOST_RETENTION_KEEP_N` | `10` | Initial published releases kept per `(project, git branch)` -- seeds the dashboard policy on first start, then managed in the UI |
| `BUILDHOST_RETENTION_RECENCY_GUARD` | `24h` | Initial recency guard (never evict releases newer than this) -- seeds the dashboard policy, then managed in the UI |
| `BUILDHOST_RETENTION_ENFORCE` | `false` | Whether the background sweeper actually deletes; default is report-only. Manual runs from the dashboard/CLI delete when you confirm regardless |

## GitHub organization webhook

Set `BUILDHOST_GITHUB_WEBHOOK_SECRET`, then create a GitHub organization webhook with:

- Payload URL: `https://buildhost.example.com/api/v1/webhooks/github`
- Content type: `application/json`
- Secret: the same value as `BUILDHOST_GITHUB_WEBHOOK_SECRET`
- Events: select **Delete** events

When GitHub sends a branch deletion (`delete` event with `ref_type: "branch"`), buildhost deletes static site deployments for that branch in the repository's project namespace. For a repository named `myrepo`, branch `feature-x` cleanup applies to `myrepo` and slash-namespaced projects below it such as `myrepo/docs`. Tag delete events and unrelated webhook events are acknowledged and ignored.

## Retention / garbage collection

<<<<<<< HEAD
buildhost can reclaim storage by evicting old releases. Eviction keeps the latest `BUILDHOST_RETENTION_KEEP_N` published releases on each `(project, git branch)` and sweeps abandoned (never-published) uploads. This is then deletes any content-addressed blob no longer referenced by anything. **Pins that are never evicted:** each branch's latest published release, any release a `docker`/OCI tag points at, pushed-docker builds. This is anything newer than `BUILDHOST_RETENTION_RECENCY_GUARD`.
=======
buildhost can reclaim storage by evicting old releases. Eviction keeps the latest `BUILDHOST_RETENTION_KEEP_N` published releases on each `(project, git branch)`. It sweeps abandoned (never-published) uploads, then deletes any content-addressed blob that nothing references. **Pins that are never evicted:** each branch's latest published release, any release a `docker`/OCI tag points at, and pushed-docker builds. Anything newer than `BUILDHOST_RETENTION_RECENCY_GUARD` is pinned too.
>>>>>>> origin/master

It is **report-only by default**. Nothing is deleted automatically. Manage it from the **admin dashboard's Retention page**. There you edit the policy, which is the keep-N value and the recency guard. You also see a live preview of exactly which releases an eviction removes, and how much storage that frees. You can also click to run garbage collection on demand, behind a confirmation. The policy is stored in the database. The `BUILDHOST_RETENTION_KEEP_N` and `_RECENCY_GUARD` env vars only seed its initial values.

For headless/automated use there is also a CLI and an opt-in background sweeper:

```bash
buildhost gc              # report what would be evicted (dry run)
buildhost gc --enforce    # actually evict and reclaim
```

Set `BUILDHOST_RETENTION_INTERVAL`, for example `1h`, to run the sweep periodically. It deletes only when `BUILDHOST_RETENTION_ENFORCE=true`. It otherwise logs the eviction it plans. The background sweeper reads the live policy from the dashboard on each run.

Blob deletion is reference-counted. Storage is deduplicated. A blob is removed only once no release, no site and no image references it.

## OIDC auto-provisioning

Set `BUILDHOST_OIDC_ISSUERS` to a comma-separated list of trusted OIDC issuers (e.g., `https://token.actions.githubusercontent.com`). When a JWT from a trusted issuer arrives and no explicit OIDC policy matches, buildhost:

1. Fetches the issuer's JWKS keys (via OIDC discovery) and verifies the JWT signature
2. Checks the org (from subject) and event type (from `event_name` claim) against the allowlists
3. Derives the repo's project name from the subject claim (`repo:org/name:*` -> `name`). A leading `.`, `_` or `-` is dropped, so `.github` -> `github`.
4. Auto-creates the project, or any project slash-namespaced beneath it, when that project does not exist. Such a project gets auto-versioning.
5. Grants `read,write` scoped to that repo's namespace: project `name` and any `name/<...>` beneath it, but nothing else

No manual project creation or OIDC policy setup needed.

### Slash-namespaced projects

A project name may contain `/`, and it nests to any depth, as `log-streamer/client` does. A repository's OIDC token owns its whole namespace. Repo `R` may create and publish `R`, and any `R/<...>` beneath it. It may never touch a sibling such as `R-evil`, and never an unrelated project.

<<<<<<< HEAD
That is what lets a repo that ships several binaries publish each one to its own project. Go-toolchain's autorelease maps every built binary to `<repo>/<binary>`. It strips a redundant leading `<repo>-`. A single binary named after the repo stays flat, as `<repo>`.
=======
A repo that ships several binaries can therefore publish each to its own project. go-toolchain's autorelease maps every built binary to `<repo>/<binary>`. It strips a redundant leading `<repo>-`. A single binary named after the repo stays flat, as `<repo>`.
>>>>>>> origin/master

| repo | binary | project |
|------|--------|---------|
| `log-streamer` | `log-streamer-client` | `log-streamer/client` |
| `log-streamer` | `log-streamer-server` | `log-streamer/server` |
| `foo` | `foo` | `foo` |
| `foo` | `foo-cli` | `foo/cli` |

```bash
BUILDHOST_OIDC_ISSUERS=https://token.actions.githubusercontent.com \
  BUILDHOST_OIDC_ORGS=wow-look-at-my,PazerOP \
  buildhost serve
```

By default the `push`, `pull_request` and `workflow_dispatch` events are allowed. Each one limits auto-provisioning to a user with write access to the repository.

A `push` comes from a member or a collaborator. A `pull_request` from a fork receives no OIDC token at all, so only a same-repo PR, from a member, can authenticate. Only a user with write access to the repo can trigger a `workflow_dispatch`, which is a manual run. It therefore carries the same write-access guarantee as a `push`.

`pull_request` is included by default so a PR-preview deploy works out of the box. `workflow_dispatch` is included so a manual release or publish dispatch works out of the box. Set `BUILDHOST_OIDC_EVENTS=*` to allow every event type.

If `BUILDHOST_OIDC_ORGS` is empty, no orgs are allowed. Use `*` to allow all orgs. Org names are matched case-insensitively (GitHub logins are), so `pazerop` and `PazerOP` are equivalent.

## License

MIT
