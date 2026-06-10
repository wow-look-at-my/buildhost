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

buildhost exposes a generated Homebrew tap as a Git repository. Add the tap once,
then install formulas through the tap name:

```bash
brew tap pazer/build https://brew.pazer.build/tap.git
brew install pazer/build/go-toolchain
```

Do not install formulas with a naked remote URL such as
`brew install https://brew.pazer.build/go-toolchain`; modern Homebrew treats that
as a formula or tap name instead of cloning it as a formula URL.

## Web frontend

buildhost serves a public, read-only browse UI on the main domain (no subdomain). It is plain server-rendered HTML with **no JavaScript**, so it is consumable and indexable by crawlers and agents without evaluating a single-page app.

- `GET /` &mdash; index of every public project
- `GET /projects/{project}` &mdash; a project's metadata, published releases, deployed static sites, and copy-paste install/download commands
- `GET /projects/{project}/releases/{version}` &mdash; a release's artifacts with per-format download links (`raw`, `tar.gz`, `tar.xz`, `tar.zst`, `zip`), or a `docker pull` for image releases

Private projects are hidden: they are never listed for anonymous visitors, and visiting one's page directly returns a `404` &mdash; identical to a project that does not exist, so the frontend never reveals that a private project exists (the same way GitHub treats private repositories). A read-scoped token authorized for the project reveals it. Download links point at the `dl` subdomain; the single stylesheet is served from `/_ui/style.css` and no other assets are loaded. The authenticated admin dashboard remains a separate app on its own port (see [Container image](#container-image)).

## Synthesized container images

When a project has only a plain binary (no pushed image), buildhost synthesizes an OCI
image from it on demand -- `docker pull` / `crane pull` just works. The image is
deliberately minimal but ships the runtime essentials of `gcr.io/distroless/static`, so a
networked service works without pushing a real image:

- A real public **CA certificate bundle** at `/etc/ssl/certs/ca-certificates.crt` (and
  `SSL_CERT_FILE` pointing at it), so outbound HTTPS works -- no more
  `x509: certificate signed by unknown authority`.
- `/etc/passwd` and `/etc/group` with `root`, `nobody` and `nonroot` (UID 65532), an
  `/etc/nsswitch.conf` (`hosts: files dns`) and a sticky `/tmp`.
- The binary at `/<project>` as the entrypoint, a sane `PATH`, and `WorkingDir=/`.

The image runs as **root** by default. To run as another user, set `oci_user` on the
release (`uid[:gid]` or `name[:group]`, e.g. `65532:65532` for the bundled nonroot user);
it is emitted as the image's `config.User`:

```bash
buildhost publish --oci-user 65532:65532 ...   # or oci_user in a release manifest / the
                                               # oci_user field of the create-release JSON
```

The synthesized image is regenerated on demand (not stored), so its digest is not pinned
and may change between buildhost versions.

## Publishing real Docker images

Some projects need to ship a real prebuilt image (custom base image, native
libraries, entrypoint, exposed ports) rather than a binary wrapped in a minimal
layer. buildhost is a writable OCI registry, so you can `docker push` directly:

```bash
docker login builds.example.com -u oidc -p "$TOKEN"   # any username; password is a write-scoped token
docker buildx build --push -t builds.example.com/myproject:v1.2.3 .
docker pull builds.example.com/myproject:v1.2.3
```

A release that contains a pushed image is a **docker build**: it is served only
through the OCI (`/v2`) endpoint. The apt/brew/npm and raw-download endpoints do
not apply to it -- it is just a container image. Pushed image layers are
content-addressed and deduplicated, so unchanged layers are not re-uploaded on
later pushes. Per-blob size is capped by `BUILDHOST_MAX_BLOB_SIZE` (default 10 GiB).

### From GitHub Actions

Use the `buildhost-publish-docker` action to build and push in one step,
authenticating with a GHA OIDC token (no static secret -- the project
auto-provisions on first push):

```yaml
permissions:
  id-token: write   # required to mint the OIDC token
  contents: read
steps:
  - uses: actions/checkout@v4
  - uses: wow-look-at-my/buildhost/.github/actions/buildhost-publish-docker@master
    with:
      server: https://builds.example.com   # optional, defaults to https://pazer.build
      context: .                            # optional
      # tags default to the commit SHA and "latest"; bare tags are expanded to
      # <registry>/<project>:<tag>, full references (with "/" or ":") are used as-is.
      tags: |
        ${{ github.sha }}
        latest
```

For a build you drive yourself (e.g. `docker buildx imagetools` to copy an
existing multi-arch image), use the lower-level `buildhost-docker-login` action,
which only performs the OIDC `docker login`, and then run your own docker
commands.

## Container image

A container image is published to `ghcr.io/wow-look-at-my/buildhost:latest` on every push to master.

The image is based on `gcr.io/distroless/static-debian12:nonroot` and runs as UID 65532. It contains:

- `/usr/local/bin/buildhost` -- the statically linked binary
- CA certificates (from distroless base)
- `/etc/passwd` with `nonroot` user (UID 65532)

No shell, no package manager, no other binaries.

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

**Note:** Binary stripping (`strip`/`objcopy`) is not available in the hardened image. Uploaded binaries are served as-is. If you need debug info stripping, run it in your CI pipeline before uploading.

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

## Versioning

Projects use auto-incrementing versions by default (v1, v2, v3...). Opt into semver with `--versioning semver` at project creation.

Git branch and commit are tracked on every release. Download the latest build of a branch:

```
GET /dl/myapp/branch/main/linux/amd64
```

`latest` (no branch) resolves to the newest published release on the project's **default branch**, so a push to a feature branch never hijacks `latest`. The default branch is recorded from the create-release `default_branch` field, which the publish actions derive from the GitHub repo. Until a default branch is recorded (or if it has no published release yet), `latest` falls back to the newest release across all branches.

## Static sites

Host small, self-contained static sites with independent per-branch deployments. Each branch gets its own site that exists from first deploy until explicitly deleted.
Directory requests serve `index.html`. If a requested file is missing and the uploaded site contains a root `404.html`, buildhost serves that page with HTTP 404.

```bash
# Deploy a site from a directory
buildhost publish-site \
  --server http://localhost:8080 \
  --token $TOKEN \
  --project myapp \
  --branch main \
  --dir ./dist

# The site is available at:
# http://localhost:8080/sites/myapp/branch/main/

# Re-deploying the same branch replaces the previous site atomically.
# Deleting a branch deployment:
curl -X DELETE -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/sites/myapp/branch/main
```

## Tokens

Tokens authenticate all API requests. There are two kinds:

- **Global tokens** (`project_id` omitted): can access all projects and manage tokens.
- **Project-scoped tokens** (`project_id` set): limited to one project; cannot list or delete tokens.

Each token has a `scopes` field: `read`, `write`, or `read,write`. The default when omitted is `read`. A token can only grant scopes it already holds — a read-only token cannot mint a write token.

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

All three forms are equivalent:

```bash
# Bearer token (preferred)
curl -H "Authorization: Bearer $TOKEN" https://buildhost.example.com/api/v1/projects

# Basic auth (password field is the token; username is ignored)
curl -u "token:$TOKEN" https://buildhost.example.com/api/v1/projects

# Query parameter (for clients that cannot set headers, e.g. APT, Brew)
curl "https://buildhost.example.com/api/v1/projects?token=$TOKEN"
```

## API

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/tokens` | Create token |
| GET | `/api/v1/tokens` | List tokens (global token required) |
| DELETE | `/api/v1/tokens/{id}` | Delete token (global token required) |
| POST | `/api/v1/projects` | Create project |
| GET | `/api/v1/projects` | List projects |
| POST | `/api/v1/projects/{project}/releases` | Create release |
| PUT | `/api/v1/projects/{project}/releases/{version}/artifacts/{os}/{arch}` | Upload artifact |
| POST | `/api/v1/projects/{project}/releases/{version}/publish` | Publish release |
| POST | `/api/v1/webhooks/github` | GitHub org webhook receiver for branch deletion cleanup |
| GET | `/dl/{project}/{version}/{os}/{arch}` | Download |
| GET | `/dl/{project}/latest/{os}/{arch}` | Download latest |
| GET | `/dl/{project}/branch/{branch}/{os}/{arch}` | Download latest for branch |
| PUT | `/sites/{project}/branch/{branch}` | Deploy static site (tar.gz body) |
| DELETE | `/sites/{project}/branch/{branch}` | Remove static site |
| GET | `/sites/{project}/branch/{branch}/{path}` | Serve static site file |
| GET | `/api/v1/projects/{project}/sites` | List branch deployments |
| GET | `/llms.txt` | Plain-text guide to buildhost for LLMs ([llmstxt.org](https://llmstxt.org)) |
| GET | `/healthz` | Liveness check (database ping); JSON body reports the running build's commit and version |
| GET | `/` | Public read-only web frontend: index of public projects |
| GET | `/projects/{project}` | Web frontend: project page (releases, install commands) |
| GET | `/projects/{project}/releases/{version}` | Web frontend: release page (artifacts + download links) |

## llms.txt

`GET /llms.txt` serves a public, unauthenticated plain-text document that explains what buildhost is and how to use it, aimed at LLMs and automated agents. Example URLs in the document are rendered against the request's `Host`, so they always point at the live deployment.

## Health and version

`GET /healthz` returns `200` when the server is up and its database is reachable, and `503` when the database is unreachable. Either way the JSON body reports the exact build the server is running, so you can check which image a deployment is on:

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
| `BUILDHOST_OIDC_EVENTS` | `push,pull_request` | Comma-separated allowed event types for OIDC auto-provisioning (`*` for all) |
| `BUILDHOST_GITHUB_WEBHOOK_SECRET` | (off) | Enables `POST /api/v1/webhooks/github`; used to verify GitHub webhook HMAC signatures |
| `BUILDHOST_RETENTION_INTERVAL` | (off) | Background GC sweep cadence (e.g. `1h`); empty/`0` disables the sweeper |
| `BUILDHOST_RETENTION_KEEP_N` | `10` | Initial published releases kept per `(project, git branch)` -- seeds the dashboard policy on first start, then managed in the UI |
| `BUILDHOST_RETENTION_RECENCY_GUARD` | `24h` | Initial recency guard (never evict releases newer than this) -- seeds the dashboard policy, then managed in the UI |
| `BUILDHOST_RETENTION_ENFORCE` | `false` | Whether the background sweeper actually deletes; default is report-only. Manual runs from the dashboard/CLI delete when you confirm regardless |

## GitHub organization webhook

Set `BUILDHOST_GITHUB_WEBHOOK_SECRET`, then create a GitHub organization webhook
with:

- Payload URL: `https://buildhost.example.com/api/v1/webhooks/github`
- Content type: `application/json`
- Secret: the same value as `BUILDHOST_GITHUB_WEBHOOK_SECRET`
- Events: select **Delete** events

When GitHub sends a branch deletion (`delete` event with `ref_type: "branch"`),
buildhost deletes static site deployments for that branch in the repository's
project namespace. For a repository named `myrepo`, branch `feature-x` cleanup
applies to `myrepo` and slash-namespaced projects below it such as `myrepo/docs`.
Tag delete events and unrelated webhook events are acknowledged and ignored.

## Retention / garbage collection

buildhost can reclaim storage by evicting old releases. Eviction keeps the latest
`BUILDHOST_RETENTION_KEEP_N` published releases on each `(project, git branch)` and
sweeps abandoned (never-published) uploads, then deletes any content-addressed blob
no longer referenced by anything. **Pins that are never evicted:** each branch's
latest published release, any release a `docker`/OCI tag points at, pushed-docker
builds, and anything newer than `BUILDHOST_RETENTION_RECENCY_GUARD`.

It is **report-only by default** -- nothing is deleted automatically. Manage it
from the **admin dashboard's Retention page**: edit the policy (keep-N and recency
guard), see a live preview of exactly which releases would be evicted and how much
storage that frees, and click to run garbage collection on demand (with a
confirmation). The policy is stored in the database; the `BUILDHOST_RETENTION_KEEP_N`
/ `_RECENCY_GUARD` env vars only seed its initial values.

For headless/automated use there is also a CLI and an opt-in background sweeper:

```bash
buildhost gc              # report what would be evicted (dry run)
buildhost gc --enforce    # actually evict and reclaim
```

Set `BUILDHOST_RETENTION_INTERVAL` (e.g. `1h`) to run the sweep periodically; it
only deletes if `BUILDHOST_RETENTION_ENFORCE=true` (otherwise it just logs what it
would do). The background sweeper reads the live policy from the dashboard each run.

Blob deletion is reference-counted: because storage is deduplicated, a blob is
removed only once no release, site, or image references it.

## OIDC auto-provisioning

Set `BUILDHOST_OIDC_ISSUERS` to a comma-separated list of trusted OIDC issuers (e.g., `https://token.actions.githubusercontent.com`). When a JWT from a trusted issuer arrives and no explicit OIDC policy matches, buildhost:

1. Fetches the issuer's JWKS keys (via OIDC discovery) and verifies the JWT signature
2. Checks the org (from subject) and event type (from `event_name` claim) against the allowlists
3. Derives the repo's project name from the subject claim (`repo:org/name:*` -> `name`)
4. Auto-creates the project — or any project slash-namespaced beneath it — if it doesn't exist (with auto-versioning)
5. Grants `read,write` scoped to that repo's namespace: project `name` and any `name/<...>` beneath it, but nothing else

No manual project creation or OIDC policy setup needed.

### Slash-namespaced projects

Project names may contain `/` and nest to any depth (e.g. `log-streamer/client`). A repository's OIDC token owns its whole namespace: repo `R` may create and publish `R` and any `R/<...>`, but never a sibling like `R-evil` or an unrelated project. This is what lets a repo that ships several binaries publish each to its own project — go-toolchain's autorelease maps every built binary to `<repo>/<binary>`, stripping a redundant leading `<repo>-` (a single binary named after the repo stays flat as `<repo>`):

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

By default, `push` and `pull_request` events are allowed. Both limit auto-provisioning to users with write access to the repository: a `push` comes from a member/collaborator, and a `pull_request` from a fork does not receive an OIDC token at all (so only same-repo PRs, i.e. members, can authenticate). `pull_request` is included by default so PR-preview deploys work out of the box. Set `BUILDHOST_OIDC_EVENTS=*` to allow all event types.

If `BUILDHOST_OIDC_ORGS` is empty, no orgs are allowed. Use `*` to allow all orgs. Org names are matched case-insensitively (GitHub logins are), so `pazerop` and `PazerOP` are equivalent.

## License

MIT
