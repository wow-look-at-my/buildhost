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
- **OCI/Docker registry** (minimal container images synthesized from the binary)

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
    environment:
      - BUILDHOST_BASE_URL=https://builds.example.com
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

## Static sites

Host small, self-contained static sites with independent per-branch deployments. Each branch gets its own site that exists from first deploy until explicitly deleted.

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
| GET | `/dl/{project}/{version}/{os}/{arch}` | Download |
| GET | `/dl/{project}/latest/{os}/{arch}` | Download latest |
| GET | `/dl/{project}/branch/{branch}/{os}/{arch}` | Download latest for branch |
| PUT | `/sites/{project}/branch/{branch}` | Deploy static site (tar.gz body) |
| DELETE | `/sites/{project}/branch/{branch}` | Remove static site |
| GET | `/sites/{project}/branch/{branch}/{path}` | Serve static site file |
| GET | `/api/v1/projects/{project}/sites` | List branch deployments |
| GET | `/llms.txt` | Plain-text guide to buildhost for LLMs ([llmstxt.org](https://llmstxt.org)) |

## llms.txt

`GET /llms.txt` serves a public, unauthenticated plain-text document that explains what buildhost is and how to use it, aimed at LLMs and automated agents. Example URLs in the document are rendered against the configured `BUILDHOST_BASE_URL`, so they always point at the live deployment.

## Configuration

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `BUILDHOST_LISTEN_ADDR` | `:8080` | API listen address |
| `BUILDHOST_ADMIN_LISTEN_ADDR` | `:9090` | Admin dashboard listen address (empty to disable) |
| `BUILDHOST_DATA_DIR` | `./data` | Data directory |
| `BUILDHOST_DB_PATH` | `./data/buildhost.db` | SQLite database path |
| `BUILDHOST_BASE_URL` | `http://localhost:8080` | External URL for generated links |
| `BUILDHOST_OIDC_ISSUERS` | (none) | Comma-separated trusted OIDC issuers for auto-provisioning |
| `BUILDHOST_OIDC_ORGS` | (none) | Comma-separated allowed orgs for OIDC auto-provisioning (`*` for all) |
| `BUILDHOST_OIDC_EVENTS` | `push` | Comma-separated allowed event types for OIDC auto-provisioning (`*` for all) |

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

By default, only `push` events are allowed, which limits auto-provisioning to users with write access to the repository (org members/collaborators). Set `BUILDHOST_OIDC_EVENTS=*` to allow all event types.

If `BUILDHOST_OIDC_ORGS` is empty, no orgs are allowed. Use `*` to allow all orgs.

## License

MIT
