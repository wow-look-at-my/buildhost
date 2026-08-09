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

buildhost exposes a generated Homebrew tap as a Git repository. Add the tap
once, trust it, then install formulas through the tap name. `brew trust` is
required since Homebrew 6.0, which refuses to evaluate third-party taps until
they are trusted (older brews have no `trust` command and enforce nothing):

```bash
brew tap pazer/build https://brew.pazer.build/tap.git
brew trust pazer/build
brew install pazer/build/go-toolchain
```

Do not install formulas with a naked remote URL such as
`brew install https://brew.pazer.build/go-toolchain`; modern Homebrew treats that
as a formula or tap name instead of cloning it as a formula URL.

On Linux, these formulas have no bottles, so `brew install` runs Homebrew's
build sandbox: it needs bubblewrap (`apt install bubblewrap`; Homebrew also
installs its own) and unprivileged user namespaces -- hardened hosts such as
Ubuntu 24.04 may need `sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0`.
In containers or CI where user namespaces are unavailable, set
`HOMEBREW_NO_SANDBOX_LINUX=1` instead. macOS needs neither.

A slash-namespaced project folds `/` to `-` in its formula name (the same rule
APT applies to package names): project `log-streamer/client` installs as
`brew install pazer/build/log-streamer-client`. A project whose name starts
with a digit cannot be served as a formula at all -- Homebrew derives the
Ruby class from the formula name and a Ruby class cannot start with a digit --
so such projects are omitted from the tap.

### Private projects

A private project never appears in the public tap. Tap the **authenticated
tap** instead: it serves every public formula plus the private projects your
token can read, so it replaces the public tap under the same name (if you
already added the public tap, remove it first with
`brew untap --force pazer/build`). Git only transmits credentials after a 401
challenge, so the token goes in the tap URL as the HTTP Basic password (the
username is ignored; `x` by convention). Artifact downloads authenticate
separately through `HOMEBREW_BUILDHOST_TOKEN`, which private formulas read at
install time -- the token is never written into the tap. The example below is
a private project named `myrepo/myapp`; per the folding rule above it
installs as `myrepo-myapp`, and the installed command keeps the binary's own
name (`myapp`):

```bash
brew tap pazer/build "https://x:$TOKEN@brew.pazer.build/private/tap.git"
brew trust pazer/build
export HOMEBREW_BUILDHOST_TOKEN="$TOKEN"
brew install pazer/build/myrepo-myapp
```

`brew update` refreshes the tap with the credentials stored in the tap's git
remote. The `?token=` query parameter cannot be used with `brew tap`: git
appends its own path segments (`/info/refs`, ...) after the query string, so
the URL stops resolving as a git repository.

### Background services (create_service)

A project can declare that its binary runs as a background service. Declare
it in the publishing repo's CI: `create_service: 'true'` on the
`buildhost-create-release` or `buildhost-publish` action (go-toolchain's
composite: `autorelease_args: create_service=true`). Every publish asserts
the declared value; an absent input leaves the stored setting untouched.
Operators can also flip it directly: `PATCH /api/v1/projects/{project}` with
`{"create_service": true}`.

Each install format materializes the setting its own way. Homebrew formulas
gain a `service do` block; activating it is one one-time command, after which
it starts at login and survives upgrades (the block runs the `opt` path):

```bash
brew services start pazer/build/competent-search-thing
```

Homebrew cannot run that for you at install: a formula's only install-time
hook (`post_install`) runs inside brew's sandbox, whose profile denies all
file writes outside build paths -- `~/Library/LaunchAgents` included -- so no
formula can register a LaunchAgent. `brew uninstall` does not stop services
either: run `brew services stop <tap>/<project>` before removing.

The service restarts only after a crash (`keep_alive successful_exit: false`;
a clean exit stays exited) and logs to `$(brew --prefix)/var/log/<name>.log`.
On Linux prefer the APT install below -- brew's Linux units carry no
graphical-session ordering. The deb materialization (which does auto-enable)
is described in the APT section; other formats (raw, zip, npm, OCI) store the
flag without materializing it.

## APT (Debian / Ubuntu)

buildhost serves each project as its own GPG-signed APT repository at
`apt.<domain>/<project>` (suite `stable`, component `main`). Packages are
generated on demand from the uploaded binary -- nothing is pre-built.

The fastest way to add a repository is the generated per-project installer. It
saves the armored signing key to `/etc/apt/keyrings/`, writes a `signed-by`
source, and refreshes the package index (APT reads the armored key directly via
`signed-by`, so no `gpg` binary is needed on the client):

```bash
curl -fsSL https://apt.pazer.build/myapp/install.sh | sudo sh
sudo apt-get install myapp
```

For a private project, pass a read token -- the installer also records it in
`/etc/apt/auth.conf.d/`, covering both the apt host and the static host the
`.deb` download redirects to:

```bash
curl -fsSL -H "Authorization: Bearer $TOKEN" https://apt.pazer.build/myapp/install.sh \
  | sudo BUILDHOST_TOKEN=$TOKEN sh
```

One-line install commands (and per-project copy buttons) are also available on
the admin dashboard: see each project's page or the **Registries** tab.

Self-modifying binaries (Cosmopolitan APEs, which rewrite their own file the
first time they run) are packaged with a launcher: the binary installs under
`/usr/lib/<pkg>/` and `/usr/bin/<pkg>` keeps a writable per-user copy, so an
ordinary user can run it. Everything else installs straight to `/usr/bin`.

Prefer to set it up by hand? Import the repository signing key once, add the
source, then install. The key is served per project path but is the same
server-wide key:

```bash
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://apt.pazer.build/myapp/key.asc \
  | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg
echo "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] https://apt.pazer.build/myapp stable main" \
  | sudo tee /etc/apt/sources.list.d/myapp.list
sudo apt update && sudo apt install myapp
```

### Private projects

A private project requires a token on every APT request. Put it in an
`apt.conf.d`-style auth file so both `apt update` and the package download
(which redirects to the `static` subdomain) authenticate. buildhost reads the
token from the HTTP Basic **password** field (the username is ignored, so any
value works -- `token` is used here by convention):

```bash
sudo install -d -m 0755 /etc/apt/keyrings
# key.asc is itself gated for a private project, so authenticate the key fetch too
curl -fsSL -u "token:$TOKEN" https://apt.pazer.build/myapp/key.asc \
  | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg
echo "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] https://apt.pazer.build/myapp stable main" \
  | sudo tee /etc/apt/sources.list.d/myapp.list
cat <<EOF | sudo tee /etc/apt/auth.conf.d/buildhost.conf >/dev/null
machine apt.pazer.build login token password $TOKEN
machine static.pazer.build login token password $TOKEN
EOF
sudo chmod 600 /etc/apt/auth.conf.d/buildhost.conf
sudo apt update && sudo apt install myapp
```

### Slash-namespaced projects

A Debian package name cannot contain `/` (or `_`), so for a slash-namespaced
project the package name folds those characters to `-`. Project
`pr-reviewer-agent/server` is served at `apt.pazer.build/pr-reviewer-agent/server`
(the slash stays in the repository URL) but installs as the package
**`pr-reviewer-agent-server`**, and the binary lands at
`/usr/bin/pr-reviewer-agent-server`:

```bash
echo "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] https://apt.pazer.build/pr-reviewer-agent/server stable main" \
  | sudo tee /etc/apt/sources.list.d/pr-reviewer-agent-server.list
sudo apt update && sudo apt install pr-reviewer-agent-server
```

### Background services (create_service)

A `create_service` project's generated deb (see the Homebrew section for the
flag itself) ships a systemd user unit at
`/usr/lib/systemd/user/<pkg>.service` -- crash-only `Restart=on-failure`,
bound to `graphical-session.target` -- and sets it up at install: the
package's postinst runs `systemctl --global enable`, so the service starts at
every user's next graphical login (plus a best-effort immediate start for the
installing sudo user's live session). Removing the package disables it again.

This applies to buildhost-GENERATED debs only (`fmt=deb`, i.e. this APT
repository). A pre-built `.deb` uploaded as an artifact (`kind=archive`) is
served byte-identical -- buildhost never injects into uploaded files.

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

The OCI registry is served on the `oci.` subdomain (the apex host serves the
API, not `/v2/`):

```bash
docker login oci.builds.example.com -u oidc -p "$TOKEN"   # any username; password is a write-scoped token
docker buildx build --push -t oci.builds.example.com/myproject:v1.2.3 .
docker pull oci.builds.example.com/myproject:v1.2.3
```

A release that contains a pushed image is a **docker build**: it is served only
through the OCI (`/v2`) endpoint. The apt/brew/npm and raw-download endpoints do
not apply to it -- it is just a container image. Pushed image layers are
content-addressed and deduplicated, so unchanged layers are not re-uploaded on
later pushes. Per-blob size is capped by `BUILDHOST_MAX_BLOB_SIZE` (default 10 GiB).

If a proxy in front of the server caps request bodies (Cloudflare's edge 413s
bodies over ~100 MB), `docker push` fails on big layers -- docker/buildx send
each layer as one request. Push through the CLI instead: it uploads blobs in
chunks sized under the server's advertised limit, so any layer size goes through.

```bash
docker buildx build --output type=oci,dest=image.tar -t oci.builds.example.com/myproject:v1.2.3 .
buildhost docker-push --token "$TOKEN" --image image.tar oci.builds.example.com/myproject:v1.2.3
```

### From GitHub Actions

Use the `buildhost-publish-docker` action to build and push in one step,
authenticating with a GHA OIDC token (no static secret -- the project
auto-provisions on first push):

```yaml
permissions:
  id-token: write   # required to mint the OIDC token
  contents: read
  deployments: write   # optional, additive: register the publish as a GitHub Deployment
steps:
  - uses: actions/checkout@v4
  - uses: wow-look-at-my/buildhost/.github/actions/buildhost-publish-docker@master
    with:
      server: https://builds.example.com   # optional, defaults to https://pazer.build
      context: .                            # optional
```

With `tags` omitted, pushes are tagged with the commit SHA and the sanitized
branch name (`claude/foo` -> `claude-foo`); `latest` is added only on the
default branch, so a feature branch never moves the `:latest` pointer.

Pass `tags` (newline-separated) to override: bare tags expand to
`<registry>/<project>:<tag>`; references containing `/` or `:` are used
as-is, so you can also push to another registry you are logged in to.

To fetch an artifact back in a workflow, `buildhost-download` resolves the same
way the URL does and defaults to the runner's own platform:

```yaml
- uses: wow-look-at-my/buildhost/.github/actions/buildhost-download@master
  id: cli
  with:
    project: buildhost      # optional: version, branch, os, arch, format, token
```

It outputs `path`. With `required: 'false'` a missing artifact sets
`downloaded: 'false'` instead of failing, so a caller can fall back.

For a build you drive yourself, `buildhost-docker-push` takes an OCI layout you
already produced and pushes it in chunks, so a layer over the proxy's body cap
still goes through. Obtaining a CLI that can do that is the action's problem,
not yours:

```yaml
- run: docker buildx build --output type=oci,tar=false,dest=layout .
- uses: wow-look-at-my/buildhost/.github/actions/buildhost-docker-push@master
  with:
    image: layout
    refs: oci.pazer.build/myproject:v1.2.3
```

Publishing logs docker in for you. When you need the credential for something
else -- a plain `docker push`, or pulling a published image back -- run
`buildhost docker-login --server <url>` immediately before that command, rather
than once at the top of the job: the OIDC token behind it is short-lived, and a
long build outlives it.

## GitHub Deployments

The top-level publish actions -- `buildhost-publish`, `buildhost-publish-site`,
and `buildhost-publish-docker` -- register each publish as a GitHub Deployment
in the calling repo: it appears in that repo's Environments/Deployments UI with
a "View deployment" link to the live buildhost URL (the release page, the site
URL, or the project page).

- On by default (`create_deployment: 'true'`), but it needs `deployments: write`
  in the calling job -- without it the step warns and the publish proceeds.
- `deployments: write` is **additive** to each action's existing permissions
  (keep `id-token: write` etc.). A job-level `permissions:` block replaces the
  workflow-level one, so list the full set wherever you declare one.
- Environments auto-name as `buildhost/<project>` (sites:
  `buildhost/<project>/<branch>`); override with `deployment_environment`.
- Set `create_deployment: 'false'` to opt out (and silence the warning).

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

**Note:** The server reads the container's memory cgroup at startup and sets `GOMEMLIMIT` to ~90% of it (via [automemlimit](https://github.com/KimMachineGun/automemlimit)), and all download/repackage paths stream (blob reads are mmap-backed, nothing buffers a whole artifact), so buildhost serves artifacts far larger than `mem_limit` without OOM-ing. Set `GOMEMLIMIT` yourself (or `AUTOMEMLIMIT=off`) to override.

**Note:** ELF binaries are stripped on download and their symbols served separately at `?fmt=symbols`; `?debug=1` returns the artifact exactly as uploaded. Stripping runs in-process, so it needs no `strip`/`objcopy` in the image. Anything that is not an ELF — a Cosmopolitan APE, a Mach-O, a script — is always served byte-for-byte as uploaded.

**Note:** Serving through Cloudflare's proxy caps request bodies at the edge (100 MB on the Free plan); [`deploy/DIRECT-INGRESS.md`](deploy/DIRECT-INGRESS.md) adds an opt-in direct TLS ingress so uploads of any size work in a single request.

## Draft releases

A release you upload but do not publish is a **draft**: it is downloadable by
its exact version and nothing else sees it — `latest`, per-branch downloads,
Homebrew, APT, npm and OCI all resolve published releases only. Use it to put a
build somewhere you can `curl` it without moving the pointer everyone follows.

```bash
buildhost publish --draft --server https://buildhost.example.com --token $TOKEN \
  --project myapp --os linux --arch amd64 --artifact ./myapp
# draft myapp/7 (not published)
# download: https://static.buildhost.example.com/file?project=myapp&v=7&os=linux&arch=amd64
```

Publish it later (`POST /api/v1/projects/{project}/releases/{version}/publish`)
and it joins the release stream, clearing the draft flag. Drafts are kept until
you delete them: retention sweeps *unpublished* releases as abandoned uploads,
but never drafts.

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

A single uploaded binary can be published for several OSes/architectures in one
request -- built for [Cosmopolitan/APE](https://justine.lol/ape.html) binaries
that run everywhere, but usable for any platform-independent artifact. The
upload endpoint's `{os}` path segment accepts a single OS (unchanged), a
comma-separated list (`linux,darwin,windows`), or the alias `cosmo` (synonyms:
`any`, `all`, `universal`) which expands to `linux`+`darwin`+`windows`. `{arch}`
likewise accepts a list or `any`/`all` for `amd64`+`arm64`:

```bash
# One APE binary, published for linux, darwin, and windows in one request
buildhost publish \
  --server http://localhost:8080 --token $TOKEN \
  --project myapp --os cosmo --arch amd64 \
  --artifact ./myapp.com

# Explicit list, full arch matrix (os x arch combinations)
buildhost publish ... --os linux,windows --arch any --artifact ./myapp
```

The body is streamed to content-addressed storage once; each os/arch
combination becomes an ordinary per-platform artifact row referencing the same
blob, so the fan-out costs database rows, not bytes. Downloads, `latest`
resolution, the APT/Brew/npm/OCI format handlers, and retention are untouched
-- there is no stored `os=any` value and no download-time fallback; clients
still download a concrete `os`/`arch`.

Details: each list element is normalized like download parameters
(`macOS` -> `darwin`, `x86_64` -> `amd64`, ...); invalid, empty, or duplicate
elements are rejected with a 400. Row creation is all-or-nothing: if any
combination already exists the whole request returns 409 naming it, and
nothing is created. A single-combination upload returns the artifact JSON
object exactly as before; a multi-combination upload returns a JSON array of
those artifact objects, in `os` list x `arch` list order. Works identically
when finalizing a [chunked upload session](#large-uploads) (one session, one
body, N rows). `kind=npm-package` keeps its literal `os=any`/`arch=any`
sentinel row and never fans out.

### Registering more slots by hash (no re-upload)

An **exact** slot set that is not an os x arch product -- say
`{linux/amd64, linux/arm64, windows/amd64}`, where `windows/arm64` must stay
free for a different native binary -- cannot be expressed with the fan-out
grammar. For that case (and to skip re-sending a byte-identical binary
entirely), upload the file once and register the remaining slots by **hash
reference**: an empty-body PUT naming the stored blob's SHA-256.

```bash
SUM=$(sha256sum ./mytool | awk '{print $1}')

# First slot carries the bytes:
curl -X PUT -H "Authorization: Bearer $TOKEN" --data-binary @./mytool \
  "https://buildhost.example.com/api/v1/projects/myapp/releases/7/artifacts/linux/amd64"

# The rest reference the stored blob -- no bytes sent:
for slot in linux/arm64 windows/amd64; do
  curl -X PUT -H "Authorization: Bearer $TOKEN" \
    "https://buildhost.example.com/api/v1/projects/myapp/releases/7/artifacts/$slot?upload_sha256=$SUM"
done
```

Semantics:

- **Check the capability first.** Only send `upload_sha256` on an empty-body
  request when `GET /api/v1/server-info` advertises
  `"upload_by_sha256": true`. A server without the capability ignores the
  parameter and stores the empty body as the artifact.
- The referenced blob must already belong to **this project** (uploaded for
  any of its releases, so re-releasing an unchanged binary is nearly free).
  An unknown hash, another project's blob, and a since-garbage-collected blob
  all return the same 404 -- fall back to a full upload.
- The created rows are ordinary artifact rows, field-for-field identical to a
  full upload's, with the same 201/409 semantics; the reference composes with
  the `{os}`/`{arch}` fan-out grammar, and each hash-ref request carries its
  own optional `X-Artifact-Filename`.
- `upload_sha256` keeps its existing meanings elsewhere: on a request **with**
  a body it is ignored, and combined with `upload_session=` it remains the
  session-finalize integrity check.

The in-repo publishers do this automatically when the server advertises the
capability: the `buildhost-publish` GitHub action and `buildhost publish
--manifest` hash the files they are about to upload, send each distinct file
once, and register byte-identical slots by reference (go-toolchain's three
identical APE slot copies transfer once instead of three times).

## WebAssembly artifacts

WebAssembly modules publish under the platform identifier `os=wasm`, with
`arch` naming the flavor: `js` for Go's browser/Node port (`GOOS=js GOARCH=wasm`)
and `wasip1` for the WASI port (`GOOS=wasip1 GOARCH=wasm`).

```bash
# Upload both Go wasm ports (one request each, or a comma list)
curl -X PUT -H "Authorization: Bearer $TOKEN" --data-binary @app.js.wasm \
  http://localhost:8080/api/v1/projects/myapp/releases/1/artifacts/wasm/js
curl -X PUT -H "Authorization: Bearer $TOKEN" --data-binary @app.wasip1.wasm \
  http://localhost:8080/api/v1/projects/myapp/releases/1/artifacts/wasm/wasip1

# Download
curl -LO "https://dl.example.com/myapp?os=wasm&arch=js"
```

For filename-derived uploads (`name_os_arch`), name the files
`myapp_wasm_js` / `myapp_wasm_wasip1`.

`os=wasm` pairs only with the `js`/`wasip1` arches and vice versa -- any other
combination (e.g. `wasm/amd64` or `linux/js`) is rejected with a 400. The
multi-platform aliases deliberately exclude wasm: `cosmo`/`any`/`all` mean
"runs on every native desktop platform", and a wasm module instead needs a JS
host or WASI runtime. Publish wasm explicitly. Wasm artifacts are served raw
and via the archive formats (`tar.gz`, `zip`, ...); they never appear in the
APT index or Homebrew tap (linux/darwin-only by construction).

**Deprecated legacy compatibility shim**: currently-released go-toolchain
autoreleases derive upload parameters from GOOS_GOARCH-ordered filenames
(`name_js_wasm` / `name_wasip1_wasm`) and therefore upload with
`os=js`/`arch=wasm` (or `os=wasip1`/`arch=wasm`). That exact pair is folded to
the canonical form at parse time -- on upload, on `dl` queries, and in the
static endpoint's canonicalization redirect -- so such uploads succeed and are
stored, listed, and served as `os=wasm`/`arch=js|wasip1`; `js` is never stored
or surfaced as an os anywhere. The shim is pair-level only (`os=js` with any
other arch, or `arch=wasm` with any other os, stays invalid) and exists for
pre-#305 go-toolchain releases; new publishers should use the canonical
`os=wasm` form.

## Versioning

Projects use auto-incrementing versions by default (v1, v2, v3...). Opt into semver with `--versioning semver` at project creation.

Git branch and commit are tracked on every release. Download the latest build of a branch:

```
GET /dl/myapp/branch/main/linux/amd64
```

`latest` (no branch) resolves to the newest published release on the project's **default branch** -- `master` by default, but buildhost detects each repo's real default branch automatically: on a GitHub Actions OIDC publish it reads the `owner/repo` from the token and asks GitHub for that repo's default branch, so a repo that releases off another branch (e.g. `v1`) gets a correct `latest` with nothing sent in the publish. A push to a feature branch never hijacks `latest`. When the default branch has no published release yet, `latest` is not available. buildhost authenticates these lookups as a **GitHub App** (`BUILDHOST_GITHUB_APP_ID` + `BUILDHOST_GITHUB_APP_PRIVATE_KEY`, PEM contents or a file path) -- recommended: short-lived installation tokens, `metadata: read` only, high rate limit. A static `BUILDHOST_GITHUB_TOKEN` PAT works as a fallback; without either, lookups are anonymous (GitHub throttles those to 60/hr/IP and cannot read private repos).

## Static sites

Host small, self-contained static sites with independent per-branch deployments. Each branch gets its own site that exists from first deploy until explicitly deleted.
Directory requests serve `index.html`. If a requested file is missing and the uploaded site contains a root `404.html`, buildhost serves that page with HTTP 404.

Sites are served on the `sites.` subdomain (like every other service); pass the
apex `--server` and the CLI derives it.

Sites are served on the `sites.` subdomain (like every other service); pass the
apex `--server` and the CLI derives it.

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

Set `BUILDHOST_SITE_DOMAIN` (e.g. `pazer.site`) to also serve each project's site
at `https://<project>.<domain>/` -- the default branch on bare paths, any other
branch (or commit) behind the `@` sigil: `https://myapp.pazer.site/@pr-7/`,
`https://myapp.pazer.site/@0f1e2d3/`.

- Slash-named branches (`claude/foo`) resolve by longest match; an `@<default-branch>`
  URL 302s to the canonical bare form. The `~` sigil this scheme launched with
  still works and 301s to the `@` form.
- Only project names that are a single DNS label serve here (`[a-z0-9-]`, max 63,
  no leading/trailing `-`); everything else stays on `sites.<apex>/...`.
- Reserved on this scheme: a leading `~` path segment and the literal `/__sso`.
- Private sites sign in via the primary apex: set `BUILDHOST_PRIMARY_DOMAIN`
  (e.g. `pazer.build`) and the browser authenticates there (same single GitHub
  OAuth app), then is handed back to the site domain -- no second OAuth app.
- Setting `BUILDHOST_PRIMARY_DOMAIN` also scopes the web UI and `/api/v1` to
  that apex: other hosts get a plain 404 (health, sign-in, `llms.txt` stay
  host-agnostic). Unset, everything stays host-agnostic as before.

## Large uploads

buildhost accepts single uploads up to 2 GiB, but a proxy in front of it may
not: Cloudflare's edge rejects request bodies over 100 MB with a 413 that
never reaches the origin. Two ways around that, both first-try reliable:

- **Direct upload endpoint (preferred when configured).** If your deployment
  exposes a hostname that reaches the origin without the proxied body cap,
  point `--server` (or your upload URLs) at it and single-request uploads of
  any size just work. Nothing else changes.
- **Hash-reference uploads (identical bytes, zero transfer).** A file that
  is byte-identical to one the project already uploaded -- another platform
  slot of the same release, or an unchanged re-release -- does not need to be
  sent at all: register it with an empty-body PUT naming the blob's SHA-256.
  See [Registering more slots by hash](#registering-more-slots-by-hash-no-re-upload).
  The in-repo publish clients do this automatically when the server
  advertises `upload_by_sha256`.
- **Chunked upload sessions (automatic fallback).** Through the proxied
  hostname, the in-repo publish clients transparently split large files into
  chunks that fit under the cap. You don't have to know this exists:
  `buildhost publish`, `buildhost publish-site`, and the
  `buildhost-upload-artifact` GitHub action all check the file size against
  the server's advertised limit (`GET /api/v1/server-info`,
  `max_direct_upload_bytes`, default 95 MiB) before sending anything, and
  switch to a session only when needed. Small files keep using the classic
  single request.

```bash
# Exactly the same command whether the file is 5 MB or 5 GB -- chunking is
# automatic when needed:
buildhost publish --server https://buildhost.example.com --token $TOKEN \
  --project myapp --os linux --arch amd64 --artifact ./huge-artifact

# Tune or disable it:
buildhost publish ... --chunk-size 32M   # smaller chunks (default 64M)
buildhost publish ... --chunk-size 0     # force a single direct request
```

Chunked uploads are resumable (each chunk is verified against the server's
committed offset; the CLI retries and resumes from the server's size on any
hiccup) and integrity-checked (the finalize step carries the file's SHA-256,
which the server verifies before accepting the artifact).

### Chunked upload session API

Sessions work with **every** upload endpoint -- artifact PUTs and site
deploys alike. Assemble the body in chunks, then call the normal endpoint
with an *empty* body and `?upload_session=<id>`; the server uses the
assembled bytes as if they were the request body.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/server-info` | Advertised limits and capabilities: `max_direct_upload_bytes`, `max_upload_bytes`, `upload_sessions`, `upload_by_sha256` (public) |
| POST | `/api/v1/uploads` | Create a session (`write` scope; bound to your identity) |
| PATCH | `/api/v1/uploads/{id}?offset=N` | Append a chunk at offset N; 409 with the committed `size` on mismatch (resume from it) |
| GET | `/api/v1/uploads/{id}` | Current committed `size` (for resuming) |
| DELETE | `/api/v1/uploads/{id}` | Abort and discard |
| any upload endpoint + `?upload_session=<id>&upload_sha256=<hex>` | | Finalize: empty body; assembled bytes become the request body (sha256 optional but recommended) |

Sessions expire after 24h (`BUILDHOST_UPLOAD_SESSION_TTL`), count against the
normal 2 GiB upload cap at append time, and only the identity that created a
session can touch it. A successful finalize consumes the session.

From GitHub Actions, the
`wow-look-at-my/buildhost/.github/actions/buildhost-upload-artifact@master`
composite does all of this automatically: it checks the advertised limit,
sends small files as the classic direct PUT (streamed from disk), assembles
larger ones through a session (default 64 MiB chunks, tunable via its
optional `chunk_size` input), resumes from the server's committed size on
hiccups, finalizes with the file's SHA-256, and retries transient
server/network errors with backoff.

From other CI without the CLI (uploading a >100 MB artifact through the
proxied hostname), the same protocol is a short curl loop:

```bash
FILE=./huge-artifact
SHA256=$(sha256sum "$FILE" | awk '{print $1}')

# 1. create a session
SESSION=$(curl -fsS -X POST -H "Authorization: Bearer $TOKEN" \
  "$SERVER/api/v1/uploads" | jq -r .id)

# 2. append 64 MB pieces at their offsets
split -b 64M "$FILE" part-
OFFSET=0
for part in part-*; do
  OFFSET=$(curl -fsS -X PATCH -H "Authorization: Bearer $TOKEN" \
    --data-binary @"$part" \
    "$SERVER/api/v1/uploads/$SESSION?offset=$OFFSET" | jq -r .size)
done

# 3. finalize: the normal upload URL, empty body, session + checksum attached
curl -fsS -X PUT -H "Authorization: Bearer $TOKEN" \
  "$SERVER/api/v1/projects/myapp/releases/$VERSION/artifacts/linux/amd64?upload_session=$SESSION&upload_sha256=$SHA256"
```

## Tokens

Tokens authenticate all API requests. There are two kinds:

- **Global tokens** (`project_id` omitted): can access all projects and manage tokens.
- **Project-scoped tokens** (`project_id` set): limited to one project; cannot list or delete tokens.

Each token has a `scopes` field, a comma-separated subset of `read`, `write`, and `share`. The default when omitted is `read`. A token can only grant scopes it already holds — a read-only token cannot mint a write token. `share` is a distinct permission to mint [temporary download links](#temporary-download-links); it is not implied by `write`, so a CI/deploy token cannot hand out shareable links to private artifacts. The bootstrap admin token holds `read,write,share`.

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

## Temporary download links

To share a single artifact from a **private** project without handing out a token, mint a temporary, signed download link. The link works for exactly one artifact (`os`/`arch`/`fmt`/`version`) and expires (default 1 hour, max 24 hours).

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

Anyone with the `url` can download that one artifact until it expires — no account or token needed. Minting requires a token with the `share` scope, authorized for the project. The admin dashboard exposes the same thing as a **"temp link"** button on each release's artifact list.

The link is a stateless HMAC signature (keyed by a server-side key generated on first start), bound to the exact artifact and expiry, so a leaked link cannot reach anything else in the project or outlive its expiry. Links are not individually revocable before expiry; rotate the signing key to invalidate all outstanding links.

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

By default, `push`, `pull_request`, and `workflow_dispatch` events are allowed. All three limit auto-provisioning to users with write access to the repository: a `push` comes from a member/collaborator, a `pull_request` from a fork does not receive an OIDC token at all (so only same-repo PRs, i.e. members, can authenticate), and a `workflow_dispatch` (a manual run) can only be triggered by a user with write access to the repo -- so it carries the same write-access guarantee as `push`. `pull_request` is included by default so PR-preview deploys work out of the box, and `workflow_dispatch` so manual release/publish dispatches work out of the box. Set `BUILDHOST_OIDC_EVENTS=*` to allow all event types.

If `BUILDHOST_OIDC_ORGS` is empty, no orgs are allowed. Use `*` to allow all orgs. Org names are matched case-insensitively (GitHub logins are), so `pazerop` and `PazerOP` are equivalent.

## License

MIT
