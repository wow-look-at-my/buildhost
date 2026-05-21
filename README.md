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
- **OCI/Docker registry** (minimal container images)

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

## API

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/projects` | Create project |
| GET | `/api/v1/projects` | List projects |
| POST | `/api/v1/projects/{project}/releases` | Create release |
| PUT | `/api/v1/projects/{project}/releases/{version}/artifacts/{os}/{arch}` | Upload artifact |
| POST | `/api/v1/projects/{project}/releases/{version}/publish` | Publish release |
| GET | `/dl/{project}/{version}/{os}/{arch}` | Download |
| GET | `/dl/{project}/latest/{os}/{arch}` | Download latest |
| GET | `/dl/{project}/branch/{branch}/{os}/{arch}` | Download latest for branch |

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

## OIDC auto-provisioning

Set `BUILDHOST_OIDC_ISSUERS` to a comma-separated list of trusted OIDC issuers (e.g., `https://token.actions.githubusercontent.com`). When a JWT from a trusted issuer arrives and no explicit OIDC policy matches, buildhost:

1. Fetches the issuer's JWKS keys (via OIDC discovery) and verifies the JWT signature
2. Derives the project name from the subject claim (`repo:org/name:*` -> `name`)
3. Auto-creates the project if it doesn't exist (with auto-versioning)
4. Grants `read,write` scope limited to that one project

No manual project creation or OIDC policy setup needed.

```bash
BUILDHOST_OIDC_ISSUERS=https://token.actions.githubusercontent.com \
  BUILDHOST_OIDC_ORGS=wow-look-at-my,PazerOP \
  buildhost serve
```

If `BUILDHOST_OIDC_ORGS` is empty, no orgs are allowed. Use `*` to allow all orgs.

## License

MIT
