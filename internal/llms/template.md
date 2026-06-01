# buildhost

> buildhost is a self-hosted universal package registry. Upload a build
> artifact once, then download it in any packaging format: raw binary,
> tar.gz / tar.xz / tar.zst, zip, an APT (.deb) repository, a Homebrew
> formula, an npm package, or an OCI/Docker image.

buildhost stores a single original binary per project, version, OS, and
architecture, and repackages it on demand at download time. Every format is
generated from that one source artifact, so they always stay in sync. All
downloads resolve to one content-addressed, CDN-cacheable endpoint with strong
ETags and immutable caching.

This document lives at `__BASE_URL__/llms.txt` and is written for LLMs and
automated agents. Every example below uses this server's configured base URL,
`__BASE_URL__`.

## Core concepts

- **Project**: a named container for releases (for example, `myapp`). Project
  names match `[a-z0-9][a-z0-9._-]{0,127}` and may contain `/` for grouping.
- **Release**: one version of a project. Versions auto-increment by default
  (`1`, `2`, `3`, ...) or use semver if the project opts in.
- **Artifact**: an uploaded binary for a specific OS and architecture, such as
  `linux/amd64`. A release can hold many artifacts.
- **Branch**: the git branch is a first-class field on every release, so you
  can always fetch the latest build of a branch.
- **Visibility**: projects are public or private. Private projects require an
  auth token on every endpoint, including the package-manager formats.

## Authentication

buildhost uses bearer tokens. Provide one in whichever way your client allows:

- HTTP header: `Authorization: Bearer <token>`
- HTTP Basic auth: use the token as the username
- Query parameter: `?token=<token>` (for clients that cannot set headers, such
  as some APT and Homebrew flows)

Tokens are global or scoped to a single project, and carry `read` and/or
`write` scopes. The default scope is `read` (least privilege). GitHub Actions
and other OIDC providers can authenticate with a short-lived JWT instead of a
static token; see the README for OIDC setup.

## Publishing with the CLI

```
# Create a project once
buildhost project create --server __BASE_URL__ --token $TOKEN --name myapp

# Publish a binary for one OS/arch (auto-creates the next version)
buildhost publish \
  --server __BASE_URL__ --token $TOKEN \
  --project myapp --os linux --arch amd64 \
  --artifact ./myapp-linux-amd64
```

## Publishing with the REST API

Create a release, upload one or more artifacts, then publish the release:

```
POST __BASE_URL__/api/v1/projects/{project}/releases
PUT  __BASE_URL__/api/v1/projects/{project}/releases/{version}/artifacts/{os}/{arch}
POST __BASE_URL__/api/v1/projects/{project}/releases/{version}/publish
```

## Downloading

Each service has its own subdomain. The download service resolves versions and
redirects to the static endpoint:

```
# The latest version (version defaults to latest when omitted)
curl -LO __DL_URL__/myapp?os=linux&arch=amd64

# A specific version
curl -LO "__DL_URL__/myapp?v=1&os=linux&arch=amd64"

# The latest build of a git branch
curl -LO "__DL_URL__/myapp?branch=main&os=linux&arch=amd64"
```

Add `&fmt=` to repackage on the fly. Supported values: `raw`, `tar.gz`,
`tar.xz`, `tar.zst`, `zip`.

```
curl -LO "__DL_URL__/myapp?os=linux&arch=amd64&fmt=tar.gz"
```

Every download request redirects to the unified, cacheable static endpoint
`__STATIC_URL__/file?project=&v=&os=&arch=&fmt=`. The static endpoint requires
a concrete version: a request with `v=latest` returns HTTP 400, so resolve the
version first (use a download URL without `v=`, or the API).

## Package managers

APT (Debian / Ubuntu). The repository is GPG-signed; see the README for the
exact signing-key setup, then add the repo and install:

```
echo "deb [signed-by=/etc/apt/keyrings/myapp.gpg] __APT_URL__/myapp stable main" \
  | sudo tee /etc/apt/sources.list.d/myapp.list
sudo apt update && sudo apt install myapp
```

Homebrew (install the generated formula directly from its URL):

```
brew install __BREW_URL__/myapp
```

npm (packages are published under the `@buildhost` scope):

```
npm install @buildhost/myapp --registry __NPM_URL__
```

OCI / Docker (the registry is served at `__OCI_URL__/v2/`):

```
docker pull __OCI_HOST__/myapp:latest
```

## Static sites

buildhost can also host small static sites, with one independent deployment per
git branch:

```
buildhost publish-site --server __BASE_URL__ --token $TOKEN \
  --project myapp --branch main --dir ./dist
# served at __SITES_URL__/myapp/branch/main/
```

## REST API reference

API routes are on the main domain. Service-specific routes use subdomains.

```
POST   /api/v1/projects                                                      create project
GET    /api/v1/projects                                                      list projects
GET    /api/v1/projects/{project}                                            get project
POST   /api/v1/projects/{project}/releases                                   create release
GET    /api/v1/projects/{project}/releases                                   list releases
GET    /api/v1/projects/{project}/releases/{version}                         get release
PUT    /api/v1/projects/{project}/releases/{version}/artifacts/{os}/{arch}   upload artifact
POST   /api/v1/projects/{project}/releases/{version}/publish                 publish release
GET    /healthz                                                              health check
```

## Notes for automated agents

- Resolve to a concrete version before calling the static endpoint; it
  rejects `v=latest` with HTTP 400.
- For private projects, send the auth token on every request, including the
  APT, Homebrew, npm, and OCI endpoints.
- `GET __BASE_URL__/healthz` returns 200 when the server and its database are
  reachable.
- The human-readable README is the authoritative reference for configuration,
  OIDC auto-provisioning, and deployment.
