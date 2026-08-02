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
- HTTP Basic auth: send the token as the password (the username is ignored)
- Query parameter: `?token=<token>` (for clients that cannot set headers,
  such as some APT flows; git -- and therefore `brew tap` -- cannot use it,
  see the Homebrew section for the private-tap mechanism)

Tokens are global or scoped to a single project, and carry `read` and/or
`write` scopes. The default scope is `read` (least privilege). GitHub Actions
and other OIDC providers can authenticate with a short-lived JWT instead of a
static token; see the README for OIDC setup.

## Publishing with the CLI

The CLI is the same binary that runs the server, published here on every build
and downloadable without a token:

```
curl -fLo buildhost "__DL_URL__/buildhost?os=linux&arch=amd64"
chmod +x buildhost
```

`os` is `linux`, `darwin` or `windows`; `arch` is `amd64` or `arm64`.

```
# Create a project once
buildhost project create --server __BASE_URL__ --token $TOKEN --name myapp

# Publish a binary for one OS/arch (auto-creates the next version)
buildhost publish \
  --server __BASE_URL__ --token $TOKEN \
  --project myapp --os linux --arch amd64 \
  --artifact ./myapp-linux-amd64

# Publish ONE binary for several platforms in one request (e.g. a
# Cosmopolitan/APE binary that runs everywhere): --os takes a comma list
# or the alias cosmo/any (= linux,darwin,windows); --arch takes a comma
# list or any (= amd64,arm64)
buildhost publish \
  --server __BASE_URL__ --token $TOKEN \
  --project myapp --os cosmo --arch amd64 \
  --artifact ./myapp.com
```

## Publishing with the REST API

Create a release, upload one or more artifacts, then publish the release:

```
POST __BASE_URL__/api/v1/projects/{project}/releases
PUT  __BASE_URL__/api/v1/projects/{project}/releases/{version}/artifacts/{os}/{arch}
POST __BASE_URL__/api/v1/projects/{project}/releases/{version}/publish
```

The upload's `{os}` segment accepts one OS, a comma-separated list
(`linux,darwin,windows`), or `cosmo` (aliases `any`/`all`/`universal`) for
linux+darwin+windows; `{arch}` accepts a list or `any`/`all` for amd64+arm64.
The body is stored once and one ordinary artifact row is created per os/arch
combination (all-or-nothing; a conflicting combination returns 409), so
downloads are unchanged -- always request a concrete os/arch. A single os/arch
returns one artifact JSON object; a multi-platform upload returns a JSON array
of them.

Hash-reference uploads: when `GET __BASE_URL__/api/v1/server-info` advertises
`"upload_by_sha256": true`, a file byte-identical to one this project already
uploaded (any release) can be registered for more os/arch slots without
re-sending it -- PUT the artifact URL with an EMPTY body and
`?upload_sha256=<hex sha256 of the file>`. The created rows and responses are
identical to a full upload's, and the reference composes with the os/arch
list grammar above. A 404 means the blob is not available to this project
(unknown, another project's, or garbage-collected): fall back to a full
upload. NEVER send an empty-body `upload_sha256` request to a server that
does not advertise the capability -- it would store the empty body as the
artifact. Together with `upload_session=` the parameter keeps its
session-integrity meaning (below).

Large uploads: a proxy in front of the server may cap single request bodies
(Cloudflare's edge rejects bodies over 100 MB). Check
`GET __BASE_URL__/api/v1/server-info` for `max_direct_upload_bytes`; anything
larger must go through a chunked upload session instead of one request:

```
POST   __BASE_URL__/api/v1/uploads                 -> {"id": ...}
PATCH  __BASE_URL__/api/v1/uploads/{id}?offset=N   append chunk at offset (repeat)
PUT    __BASE_URL__/api/v1/projects/{project}/releases/{version}/artifacts/{os}/{arch}?upload_session={id}&upload_sha256={hex}
```

The finalize step is the ORIGINAL upload endpoint with an empty body; the
assembled bytes are used as the request body. Works on the site-deploy PUT
too. Offsets must equal the committed size (a 409 returns the actual size to
resume from); `GET __BASE_URL__/api/v1/uploads/{id}` reads it and DELETE
aborts. The `buildhost publish` CLI does all of this automatically.

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

The bare download URL ("latest" -- no `v=` and no `branch=`) always resolves
the newest build of the project's default branch; uploads from other branches
never affect it. This is guaranteed and covered by buildhost's e2e regression
tests -- do not re-verify it per project.

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

APT (Debian / Ubuntu). Each project is its own GPG-signed repository. The
generated installer adds the signing key and source, then refreshes the index:

```
curl -fsSL __APT_URL__/myapp/install.sh | sudo sh
sudo apt-get install myapp
```

For a private project, pass a read token:

```
curl -fsSL -H "Authorization: Bearer $TOKEN" __APT_URL__/myapp/install.sh \
  | sudo BUILDHOST_TOKEN=$TOKEN sh
```

To set it up by hand instead, see the README. APT reads the armored key at
__APT_URL__/myapp/key.asc directly via signed-by, so no gpg step is needed.

For a slash-namespaced project the repository URL keeps the slash, but the
Debian package name folds `/` and `_` to `-` (for example, `myrepo/server` is
served at `__APT_URL__/myrepo/server` and installs as `myrepo-server`).

A `create_service` project's generated deb also ships a systemd user unit
(`/usr/lib/systemd/user/<pkg>.service`, crash-only restart, bound to the
graphical session) that the package auto-enables at install: it starts at
each user's next graphical login, and removal disables it again. Pre-built
`.deb` artifacts uploaded as `kind=archive` are served byte-identical --
never injected into.

Homebrew (tap the generated Git repository, trust it -- required since
Homebrew 6.0 -- then install; on Linux the bottle-less install runs Homebrew's
build sandbox, which needs bubblewrap and unprivileged user namespaces, or
`HOMEBREW_NO_SANDBOX_LINUX=1` in containers/CI without them):

```
brew tap pazer/build __BREW_URL__/tap.git
brew trust pazer/build
brew install pazer/build/go-toolchain
```

For a private project, tap the authenticated tap instead: it contains every
public formula plus the private projects the token can read (git transmits
credentials only after a challenge, so they ride the tap URL as the HTTP
Basic password; it replaces the public tap -- `brew untap --force pazer/build`
first if that was added), and export `HOMEBREW_BUILDHOST_TOKEN` so the
formula's download strategy can authenticate the artifact fetch. The
`?token=` query parameter cannot be used with `brew tap` -- git appends its
own path segments after the query string. Example for a private project
named `myrepo/myapp`:

```
brew tap pazer/build "__BREW_TOKEN_URL__/private/tap.git"
brew trust pazer/build
export HOMEBREW_BUILDHOST_TOKEN="$TOKEN"
brew install pazer/build/myrepo-myapp
```

A slash-namespaced project folds `/` to `-` in its formula name (the same
rule as APT package names), and the installed command keeps the binary's own
name: `myrepo/myapp` installs as `brew install pazer/build/myrepo-myapp` and
puts `myapp` on PATH.

A project that declares its `create_service` setting (a bool its CI sends on
release-create, or an operator sets via the API) is a background service,
materialized per format. Its formula carries a `service do` block: activate
once with `brew services start pazer/build/<project>` (user LaunchAgent at
login, crash-only restart, logs under `$(brew --prefix)/var/log/`; Homebrew's
sandbox makes install-time auto-start impossible for any formula, and
`brew uninstall` does not stop services -- `brew services stop` first). Its
generated deb ships an auto-enabled systemd user unit -- see the APT section.

npm (packages are published under the `@buildhost` scope):

```
npm install @buildhost/myapp --registry __NPM_URL__
```

OCI / Docker (the registry is served at `__OCI_URL__/v2/`). Public images pull
anonymously; for a private project, run `docker login __OCI_HOST__` first (any
valid token works as the password):

```
docker pull __OCI_HOST__/myapp:latest
```

## Static sites

buildhost can also host small static sites, with one independent deployment per
git branch:

```
buildhost publish-site --server __BASE_URL__ --token $TOKEN \
  --project myapp --branch main --dir ./dist
# served at __SITES_URL__/myapp/@main/
```

The project's own root path is the canonical URL: it serves the default branch
directly, so a link into a site need not name a branch at all.

```
__SITES_URL__/myapp/            -> index.html, from the default branch
__SITES_URL__/myapp/runner.html -> that file, from the default branch
```

Any other branch, or a specific commit, is named with the `@` sigil -- it cannot
occur in a project or branch name and is vanishingly rare in a file name:

```
__SITES_URL__/myapp/@pr-7/          -> the pr-7 preview
__SITES_URL__/myapp/@pr-7/x.css     -> x.css on that branch
__SITES_URL__/myapp/@0f1e2d3/x.css  -> x.css from that exact commit
```

A commit ref takes the full 40-hex sha or any abbreviation of 7+ characters. It
resolves while that commit is still the live deployment of some branch, so the
URL serves exactly that build or 404s -- it never quietly becomes a later one.

Redirects only ever run toward the shorter URL: `@<default branch>` 302s to the
bare path above, never the reverse. The original `/myapp/branch/main/x.css`
spelling is not going away either -- it 302s to whichever of the two URLs above
names the same file, so every published link keeps resolving.

Project names are slash-namespaced, so the bare path's split is resolved by
longest match: with projects `org` and `org/repo`, `__SITES_URL__/org/repo` is
org/repo's root, not the file `repo` under `org`. The `@` form never needs the
lookup -- the sigil marks exactly where the project name ends.
__SITE_SECTION__
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
GET    /api/v1/server-info                                                   upload limits (public)
POST   /api/v1/uploads                                                       create chunked upload session
GET    /api/v1/uploads/{id}                                                  session committed size
PATCH  /api/v1/uploads/{id}                                                  append chunk (?offset=N)
DELETE /api/v1/uploads/{id}                                                  abort session
GET    /healthz                                                              health check
```

## Notes for automated agents

- Resolve to a concrete version before calling the static endpoint; it
  rejects `v=latest` with HTTP 400.
- Uploads larger than server-info's `max_direct_upload_bytes` must use a
  chunked upload session (see "Publishing with the REST API"); a single
  request that big is rejected by the proxy in front of the server before it
  reaches buildhost.
- For private projects, send the auth token on every request. The APT, npm,
  and OCI endpoints accept it directly; Homebrew needs the authenticated tap
  plus `HOMEBREW_BUILDHOST_TOKEN` (see "Package managers").
- `GET __BASE_URL__/healthz` returns 200 when the server and its database are
  reachable.
- The human-readable README is the authoritative reference for configuration,
  OIDC auto-provisioning, and deployment.
