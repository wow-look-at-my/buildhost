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

buildhost exposes a generated Homebrew tap as a Git repository. Add the tap once, trust it, then install formulas through the tap name. `brew trust` is required since Homebrew 6.0, which refuses to evaluate third-party taps until they are trusted (older brews have no `trust` command and enforce nothing):

```bash
brew tap pazer/build https://brew.pazer.build/tap.git
brew trust pazer/build
brew install pazer/build/go-toolchain
```

Do not install a formula with a naked remote URL, such as `brew install https://brew.pazer.build/go-toolchain`. Modern Homebrew reads that as a formula name or a tap name. It does not clone it as a formula URL.

On Linux these formulas have no bottle. `brew install` therefore runs Homebrew's build sandbox. That sandbox needs bubblewrap, from `apt install bubblewrap`, and Homebrew also installs its own. It needs an unprivileged user namespace too. A hardened host such as Ubuntu 24.04 may need `sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0`. In a container or a CI runner with no user namespace, set `HOMEBREW_NO_SANDBOX_LINUX=1` instead. macOS needs neither.

A slash-namespaced project folds `/` to `-` in its formula name. That is the same rule APT applies to a package name. Project `log-streamer/client` therefore installs as `brew install pazer/build/log-streamer-client`.

A project whose name starts with a digit cannot be served as a formula at all. Homebrew derives the Ruby class from the formula name, and a Ruby class cannot start with a digit. Such a project is therefore omitted from the tap.

### Private projects

A private project never appears in the public tap. Tap the **authenticated tap** instead. It serves every public formula, plus the private projects your token can read. It therefore replaces the public tap under the same name. Remove the public tap first, with `brew untap --force pazer/build`, when you already added it.

Git transmits a credential only after a 401 challenge. The token therefore goes in the tap URL, as the HTTP Basic password. The username is ignored, and `x` is the convention.

An artifact download authenticates separately, through `HOMEBREW_BUILDHOST_TOKEN`. A private formula reads that at install time. The token is never written into the tap.

The example below uses a private project named `myrepo/myapp`. Under the folding rule above it installs as `myrepo-myapp`. The installed command keeps the binary's own name, `myapp`.

```bash
brew tap pazer/build "https://x:$TOKEN@brew.pazer.build/private/tap.git"
brew trust pazer/build
export HOMEBREW_BUILDHOST_TOKEN="$TOKEN"
brew install pazer/build/myrepo-myapp
```

`brew update` refreshes the tap with the credential stored in the tap's git remote. The `?token=` query parameter does not work with `brew tap`. git appends its own path segments after the query string, such as `/info/refs`. The URL then stops resolving as a git repository.

### Background services (create_service)

A project can declare that its binary runs as a background service. Declare it in the publishing repo's CI, with `create_service: 'true'` on the `buildhost-create-release` or `buildhost-publish` action. go-toolchain's composite spells that as `autorelease_args: create_service=true`. Every publish asserts the declared value. An absent input leaves the stored setting untouched. An operator can also flip it directly, with `PATCH /api/v1/projects/{project}` and a body of `{"create_service": true}`.

Each install format materializes the setting its own way. A Homebrew formula gains a `service do` block. One command activates it, once. It then starts at login, and it survives an upgrade, because the block runs the `opt` path.

```bash
brew services start pazer/build/competent-search-thing
```

Homebrew cannot run that for you at install. A formula's only install-time hook is `post_install`. That hook runs inside brew's sandbox. That profile denies every file write outside a build path, and `~/Library/LaunchAgents` is one of them. No formula can therefore register a LaunchAgent. `brew uninstall` does not stop a service either. Run `brew services stop <tap>/<project>` before you remove it.

The service restarts only after a crash, through `keep_alive successful_exit: false`. A clean exit stays exited. It logs to `$(brew --prefix)/var/log/<name>.log`. On Linux prefer the APT install below, because brew's Linux units carry no graphical-session ordering. The APT section describes the deb materialization, which does auto-enable. Every other format, meaning raw, zip, npm and OCI, stores the flag and materializes nothing.

## APT (Debian / Ubuntu)

buildhost serves each project as its own GPG-signed APT repository at `apt.<domain>/<project>` (suite `stable`, component `main`). Packages are generated on demand from the uploaded binary -- nothing is pre-built.

The fastest way to add a repository is the generated per-project installer. It saves the armored signing key to `/etc/apt/keyrings/`, writes a `signed-by` source, and refreshes the package index (APT reads the armored key directly via `signed-by`, so no `gpg` binary is needed on the client):

```bash
curl -fsSL https://apt.pazer.build/myapp/install.sh | sudo sh
sudo apt-get install myapp
```

For a private project, pass a read token. The installer also records it in `/etc/apt/auth.conf.d/`. That covers the apt host and the static host the `.deb` download redirects to.

```bash
curl -fsSL -H "Authorization: Bearer $TOKEN" https://apt.pazer.build/myapp/install.sh \
  | sudo BUILDHOST_TOKEN=$TOKEN sh
```

One-line install commands (and per-project copy buttons) are also available on the admin dashboard: see each project's page or the **Registries** tab.

A self-modifying binary is packaged with a launcher. A Cosmopolitan APE is such a binary, because it rewrites its own file the first time it runs. The binary installs under `/usr/lib/<pkg>/`. `/usr/bin/<pkg>` keeps a writable per-user copy. An ordinary user can therefore run it. Everything else installs straight to `/usr/bin`.

To set it up by hand instead, import the repository signing key once, add the source, then install. The key is served per project path. It is the same server-wide key on every path.

```bash
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://apt.pazer.build/myapp/key.asc \
  | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg
echo "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] https://apt.pazer.build/myapp stable main" \
  | sudo tee /etc/apt/sources.list.d/myapp.list
sudo apt update && sudo apt install myapp
```

### Private projects

A private project requires a token on every APT request. Put it in an `apt.conf.d`-style auth file. `apt update` and the package download then both authenticate. The package download redirects to the `static` subdomain. buildhost reads the token from the HTTP Basic **password** field. The username is ignored, so any value works, and `token` is the convention here.

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

A Debian package name cannot contain `/` or `_`. A slash-namespaced project therefore folds those characters to `-` in its package name. Project `pr-reviewer-agent/server` is served at `apt.pazer.build/pr-reviewer-agent/server`. The slash stays in the repository URL. It installs as the package **`pr-reviewer-agent-server`**. The binary lands at `/usr/bin/pr-reviewer-agent-server`.

```bash
echo "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] https://apt.pazer.build/pr-reviewer-agent/server stable main" \
  | sudo tee /etc/apt/sources.list.d/pr-reviewer-agent-server.list
sudo apt update && sudo apt install pr-reviewer-agent-server
```

### Background services (create_service)

A `create_service` project's generated deb ships a systemd user unit at `/usr/lib/systemd/user/<pkg>.service`. See the Homebrew section for the flag itself. That unit is crash-only `Restart=on-failure`. It is bound to `graphical-session.target`.

The package sets it up at install. Its postinst runs `systemctl --global enable`. The service therefore starts at every user's next graphical login. The postinst also makes a best-effort immediate start, for the installing sudo user's live session. A removal of the package disables it again.

This applies to a buildhost-GENERATED deb only, which means `fmt=deb`, from this APT repository. A pre-built `.deb` uploaded as an artifact, with `kind=archive`, is served byte-identical. buildhost never injects anything into an uploaded file.

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

A project can need a real prebuilt image, rather than a binary wrapped in a minimal layer. A custom base image, a native library, an entrypoint and an exposed port are the usual reasons. buildhost is a writable OCI registry. You can therefore `docker push` directly.

The OCI registry is served on the `oci.` subdomain. The apex host serves the API, and not `/v2/`.

```bash
docker login oci.builds.example.com -u oidc -p "$TOKEN"   # any username; password is a write-scoped token
docker buildx build --push -t oci.builds.example.com/myproject:v1.2.3 .
docker pull oci.builds.example.com/myproject:v1.2.3
```

A release that contains a pushed image is a **docker build**. The OCI `/v2` endpoint is the only place it is served. The apt, brew, npm and raw-download endpoints do not apply to it, because it is just a container image. A pushed image layer is content-addressed and deduplicated. An unchanged layer is therefore not re-uploaded on a later push. `BUILDHOST_MAX_BLOB_SIZE` caps the per-blob size, and it defaults to 10 GiB.

A proxy in front of the server may cap a request body. Cloudflare's edge answers 413 to a body near 100 MB. `docker push` then fails on a big layer, because docker and buildx send each layer as one request. Push through the CLI instead. It uploads a blob in chunks sized under the server's advertised limit, so any layer size goes through.

```bash
docker buildx build --output type=oci,dest=image.tar -t oci.builds.example.com/myproject:v1.2.3 .
buildhost docker-push --token "$TOKEN" --image image.tar oci.builds.example.com/myproject:v1.2.3
```

### From GitHub Actions

Use the `buildhost-publish-docker` action to build and push in one step. It authenticates with a GHA OIDC token, so there is no static secret. The project auto-provisions on the first push.

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

With `tags` omitted, a push is tagged with the commit SHA and the sanitized branch name, so `claude/foo` becomes `claude-foo`. `latest` is added only on the default branch. A feature branch therefore never moves the `:latest` pointer.

Pass `tags`, newline-separated, to override that. A bare tag expands to `<registry>/<project>:<tag>`. A reference that contains `/` or `:` is used as-is. You can therefore also push to another registry you are logged in to.

To fetch an artifact back in a workflow, use `buildhost-download`. It resolves the same way the URL does, and it defaults to the runner's own platform.

```yaml
- uses: wow-look-at-my/buildhost/.github/actions/buildhost-download@master
  id: cli
  with:
    project: buildhost      # optional: version, branch, os, arch, format, token
```

It outputs `path`. With `required: 'false'` a missing artifact sets `downloaded: 'false'` instead of a failure. A caller can therefore fall back.

For a build you drive yourself, use `buildhost-docker-push`. It takes an OCI layout you already produced, and pushes it in chunks. A layer over the proxy's body cap therefore still goes through. To obtain a CLI that can do that is the action's problem, and not yours.

```yaml
- run: docker buildx build --output type=oci,tar=false,dest=layout .
- uses: wow-look-at-my/buildhost/.github/actions/buildhost-docker-push@master
  with:
    image: layout
    refs: oci.pazer.build/myproject:v1.2.3
```

A publish logs docker in for you, and so does a pull. To fetch a published image, into a nested daemon or onto the runner itself, use `buildhost-docker-pull`. It authenticates itself, so no workflow handles a registry credential.

```yaml
- uses: wow-look-at-my/buildhost/.github/actions/buildhost-docker-pull@master
  with:
    images: oci.pazer.build/myproject:v1.2.3
```

Those actions are `buildhost-publish-docker`, `buildhost-docker-push` and `buildhost-docker-pull`. They are the only supported way to authenticate a CI Docker workflow to buildhost. There is no login-only action, and no CLI equivalent.

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

A single uploaded binary can be published for several operating systems and architectures, in one request. This was built for a [Cosmopolitan APE](https://justine.lol/ape.html) binary, which runs everywhere. It is usable for any platform-independent artifact.

The upload endpoint's `{os}` path segment accepts a single OS, unchanged from before. It also accepts a comma-separated list, such as `linux,darwin,windows`. It also accepts the alias `cosmo`, whose synonyms are `any`, `all` and `universal`, and which expands to `linux`, `darwin` and `windows`. The `{arch}` segment likewise accepts a list, or `any` or `all` for `amd64` and `arm64`.

```bash
# One APE binary, published for linux, darwin, and windows in one request
buildhost publish \
  --server http://localhost:8080 --token $TOKEN \
  --project myapp --os cosmo --arch amd64 \
  --artifact ./myapp.com

# Explicit list, full arch matrix (os x arch combinations)
buildhost publish ... --os linux,windows --arch any --artifact ./myapp
```

The body is streamed to content-addressed storage once. Each os/arch combination becomes an ordinary per-platform artifact row that references the same blob. The fan-out therefore costs database rows, and not bytes. Downloads, `latest` resolution, the APT, Brew, npm and OCI format handlers, and retention are all untouched. There is no stored `os=any` value, and no download-time fallback. A client still downloads a concrete `os` and `arch`.

Here are the details. Each list element is normalized like a download parameter, so `macOS` becomes `darwin` and `x86_64` becomes `amd64`. An invalid, empty or duplicate element is rejected with a 400.

Row creation is all-or-nothing. When any combination already exists, the whole request returns 409 and names it. Nothing is created then.

A single-combination upload returns the artifact JSON object exactly as before. A multi-combination upload returns a JSON array of those artifact objects, in `os` list by `arch` list order. It all works identically when you finalize a [chunked upload session](#large-uploads), with one session, one body and N rows. `kind=npm-package` keeps its literal `os=any` and `arch=any` sentinel row, and it never fans out.

### Registering more slots by hash (no re-upload)

An **exact** slot set that is not an os by arch product cannot be expressed with the fan-out grammar. `{linux/amd64, linux/arm64, windows/amd64}` is such a set, where `windows/arm64` must stay free for a different native binary. For that case, upload the file once and register the remaining slots by **hash reference**. That is an empty-body PUT that names the stored blob's SHA-256. It also skips the re-send of a byte-identical binary entirely.

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

- **Check the capability first.** Only send `upload_sha256` on an empty-body request when `GET /api/v1/server-info` advertises `"upload_by_sha256": true`. A server without the capability ignores the parameter and stores the empty body as the artifact.
- The referenced blob must already belong to **this project** (uploaded for any of its releases, so re-releasing an unchanged binary is nearly free). An unknown hash, another project's blob, and a since-garbage-collected blob all return the same 404 -- fall back to a full upload.
- The created rows are ordinary artifact rows, field-for-field identical to a full upload's, with the same 201 and 409 semantics. The reference composes with the `{os}` and `{arch}` fan-out grammar. Each hash-ref request carries its own optional `X-Artifact-Filename`.
- `upload_sha256` keeps its existing meaning elsewhere. A request **with** a body ignores it. Combined with `upload_session=` it remains the session-finalize integrity check.

The in-repo publishers do this automatically when the server advertises the capability. The `buildhost-publish` GitHub action and `buildhost publish --manifest` both hash the files they are about to upload. They send each distinct file once. They register every byte-identical slot by reference. An identical APE slot copy in go-toolchain therefore transfers once, and not once per slot.

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

Projects use auto-incrementing versions by default (v1, v2, v3...). Opt into semver with `--versioning semver` at project creation.

Git branch and commit are tracked on every release. Download the latest build of a branch:

```
GET /dl/myapp/branch/main/linux/amd64
```

`latest`, with no branch, resolves to the newest published release on the project's **default branch**. That branch is `master` by default. buildhost detects each repo's real default branch automatically. On a GitHub Actions OIDC publish it reads the `owner/repo` from the token, and asks GitHub for that repo's default branch. A repo that releases off another branch, such as `v1`, therefore gets a correct `latest` with nothing sent in the publish. A push to a feature branch never hijacks `latest`. When the default branch has no published release yet, `latest` is not available.

buildhost authenticates these lookups as a **GitHub App**. Set `BUILDHOST_GITHUB_APP_ID` and `BUILDHOST_GITHUB_APP_PRIVATE_KEY`, and the key takes the PEM contents or a file path. That path is recommended, because it mints a short-lived installation token, needs `metadata: read` only, and carries a high rate limit. A static `BUILDHOST_GITHUB_TOKEN` PAT works as a fallback. Without either, a lookup is anonymous. GitHub throttles an anonymous lookup to 60 per hour per IP, and it cannot read a private repo.

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

Set `BUILDHOST_SITE_DOMAIN` (e.g. `pazer.site`) to also serve each project's site at `https://<project>.<domain>/` -- the default branch on bare paths, any other branch (or commit) behind the `@` sigil: `https://myapp.pazer.site/@pr-7/`, `https://myapp.pazer.site/@0f1e2d3/`.

- A slash-named branch, such as `claude/foo`, resolves by longest match. An `@<default-branch>` URL 302s to the canonical bare form. The `~` sigil this scheme launched with still works, and it 301s to the `@` form.
- Only a project name that is a single DNS label serves here. That means `[a-z0-9-]`, at most 63 characters, with no leading or trailing `-`. Every other name stays on `sites.<apex>/...`.
- Reserved on this scheme: a leading `~` path segment and the literal `/__sso`.
- Private sites sign in via the primary apex: set `BUILDHOST_PRIMARY_DOMAIN` (e.g. `pazer.build`) and the browser authenticates there (same single GitHub OAuth app), then is handed back to the site domain -- no second OAuth app.
- Setting `BUILDHOST_PRIMARY_DOMAIN` also scopes the web UI and `/api/v1` to that apex: other hosts get a plain 404 (health, sign-in, `llms.txt` stay host-agnostic). Unset, everything stays host-agnostic as before.

## Large uploads

buildhost accepts a single upload up to 2 GiB. A proxy in front of it may not. Cloudflare's edge rejects a request body over 100 MB, with a 413 that never reaches the origin. Several ways around that follow, and each one is reliable on the first try.

- **The direct upload endpoint is preferred when it is configured.** Your deployment can expose a hostname that reaches the origin without the proxied body cap. Point `--server`, or your upload URLs, at that hostname. A single-request upload of any size then works. Nothing else changes.
- **A hash-reference upload sends identical bytes zero times.** A file byte-identical to one the project already uploaded need not be sent at all. Another platform slot of the same release is such a file, and so is an unchanged re-release. Register it with an empty-body PUT that names the blob's SHA-256. See [Registering more slots by hash](#registering-more-slots-by-hash-no-re-upload). The in-repo publish clients do this automatically when the server advertises `upload_by_sha256`.
- **A chunked upload session is the automatic fallback.** Through the proxied hostname, the in-repo publish clients transparently split a large file into chunks that fit under the cap. You need no knowledge that this exists. `buildhost publish`, `buildhost publish-site` and the `buildhost-upload-artifact` GitHub action all check the file size against the server's advertised limit before they send anything. That limit is `max_direct_upload_bytes` from `GET /api/v1/server-info`, and it defaults to 95 MiB. They switch to a session only when they need one. A small file keeps the classic single request.

```bash
# Exactly the same command whether the file is 5 MB or 5 GB -- chunking is
# automatic when needed:
buildhost publish --server https://buildhost.example.com --token $TOKEN \
  --project myapp --os linux --arch amd64 --artifact ./huge-artifact

# Tune or disable it:
buildhost publish ... --chunk-size 32M   # smaller chunks (default 64M)
buildhost publish ... --chunk-size 0     # force a single direct request
```

A chunked upload is resumable. Each chunk is verified against the server's committed offset. The CLI retries on any hiccup, and resumes from the server's size. The upload is integrity-checked too. The finalize step carries the file's SHA-256, and the server verifies it before it accepts the artifact.

### Chunked upload session API

A session works with **every** upload endpoint. An artifact PUT and a site deploy both qualify. Assemble the body in chunks. Then call the normal endpoint with an *empty* body and `?upload_session=<id>`. The server uses the assembled bytes as if they were the request body.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/server-info` | Advertised limits and capabilities: `max_direct_upload_bytes`, `max_upload_bytes`, `upload_sessions`, `upload_by_sha256` (public) |
| POST | `/api/v1/uploads` | Create a session (`write` scope; bound to your identity) |
| PATCH | `/api/v1/uploads/{id}?offset=N` | Append a chunk at offset N; 409 with the committed `size` on mismatch (resume from it) |
| GET | `/api/v1/uploads/{id}` | Current committed `size` (for resuming) |
| DELETE | `/api/v1/uploads/{id}` | Abort and discard |
| any upload endpoint + `?upload_session=<id>&upload_sha256=<hex>` | | Finalize: empty body; assembled bytes become the request body (sha256 optional but recommended) |

A session expires after 24h, per `BUILDHOST_UPLOAD_SESSION_TTL`. It counts against the normal 2 GiB upload cap, at append time. Only the identity that created a session can touch it. A successful finalize consumes the session.

From GitHub Actions, the `wow-look-at-my/buildhost/.github/actions/buildhost-upload-artifact@master` composite does all of this automatically. It checks the advertised limit. It sends a small file as the classic direct PUT, streamed from disk. It assembles a larger file through a session, in 64 MiB chunks by default, which its optional `chunk_size` input tunes. It resumes from the server's committed size on a hiccup. It finalizes with the file's SHA-256. It retries a transient server or network error, with backoff.

From other CI without the CLI (uploading a >100 MB artifact through the proxied hostname), the same protocol is a short curl loop:

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

The link is a stateless HMAC signature. A server-side key, generated on the first start, keys it. The signature is bound to the exact artifact and expiry. A leaked link therefore reaches nothing else in the project, and it cannot outlive its expiry. A link is not individually revocable before its expiry. Rotate the signing key to invalidate every outstanding link.

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

buildhost can reclaim storage by evicting old releases. Eviction keeps the latest `BUILDHOST_RETENTION_KEEP_N` published releases on each `(project, git branch)` and sweeps abandoned (never-published) uploads, then deletes any content-addressed blob no longer referenced by anything. **Pins that are never evicted:** each branch's latest published release, any release a `docker`/OCI tag points at, pushed-docker builds, and anything newer than `BUILDHOST_RETENTION_RECENCY_GUARD`.

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
3. Derives the repo's project name from the subject claim (`repo:org/name:*` -> `name`)
4. Auto-creates the project, or any project slash-namespaced beneath it, when that project does not exist. Such a project gets auto-versioning.
5. Grants `read,write` scoped to that repo's namespace: project `name` and any `name/<...>` beneath it, but nothing else

No manual project creation or OIDC policy setup needed.

### Slash-namespaced projects

A project name may contain `/`, and it nests to any depth, as `log-streamer/client` does. A repository's OIDC token owns its whole namespace. Repo `R` may create and publish `R`, and any `R/<...>` beneath it. It may never touch a sibling such as `R-evil`, and never an unrelated project.

That is what lets a repo that ships several binaries publish each one to its own project. go-toolchain's autorelease maps every built binary to `<repo>/<binary>`. It strips a redundant leading `<repo>-`. A single binary named after the repo stays flat, as `<repo>`.

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
