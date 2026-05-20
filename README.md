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

```bash
docker run -p 8080:8080 -v buildhost-data:/data ghcr.io/wow-look-at-my/buildhost:latest
```

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
| `BUILDHOST_ADMIN_LISTEN_ADDR` | `127.0.0.1:9090` | Admin dashboard listen address (empty to disable) |
| `BUILDHOST_DATA_DIR` | `./data` | Data directory |
| `BUILDHOST_DB_PATH` | `./data/buildhost.db` | SQLite database path |
| `BUILDHOST_BASE_URL` | `http://localhost:8080` | External URL for generated links |

## License

MIT
