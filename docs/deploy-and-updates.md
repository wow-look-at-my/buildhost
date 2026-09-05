# Graceful shutdown and update coordination

Extracted verbatim from CLAUDE.md, no wording changed.

The server handles SIGTERM/SIGINT by calling `http.Server.Shutdown` with a
5-minute timeout, allowing in-flight requests (especially large uploads) to
complete before the process exits.

For zero-downtime updates, use docker-updater's rolling update mode
(`docker-updater.rolling: "true"`) with an nginx sidecar. docker-updater starts
the new container before stopping the old one; nginx routes via Docker DNS. See
`deploy/` for an example compose stack.

**Two versions must never share the service alias.** `deploy/nginx.conf` resolves
the `buildhost-backend` alias per request, and Docker's embedded DNS returns every
address an alias resolves to and rotates them -- so any container holding that
alias is taking live traffic. docker-updater used to copy the alias onto the
replacement at CREATE time, which load-balanced clients across the old and new
IMAGE for the entire health wait: with this image's `HEALTHCHECK --interval=30s
--start-period=5s`, 5 to 35 seconds during which roughly half of all requests were
answered by the build being replaced. That is what made a publish come back
missing the `artifacts` field its own release had just added. docker-updater now
withholds the aliases until the replacement is healthy, then moves them and stops
the old container, so a request is served by exactly one version. Nothing in this
repo compensates for version skew any more -- the publish action fails on a
missing field rather than retrying.

## Ready-to-update endpoint

`GET /ready-to-update` on the main server (`:8080`) returns HTTP 200 when the
server is idle, or HTTP 503 when there are in-flight write requests. It is
designed for docker-updater's HTTP pre-update checks -- no exec into the container
(the distroless image has no shell).

### The standard `/.well-known/docker-updater/` spellings

The same two handlers also answer at the paths docker-updater discovers by
itself, with no label at all:

| Path | Same as | Answers |
|---|---|---|
| `/.well-known/docker-updater/health` | `/healthz` | 200 while the database pings, 503 otherwise |
| `/.well-known/docker-updater/pre-update` | `/ready-to-update` | 200 when idle, 503 with writes in flight |

Aliases on purpose: a second implementation of "is it healthy" is a second
answer that can disagree with the first. Both are registered `HandleRaw` (no
project auth -- the prober carries no credential, and a 401 would read as
"serving but permanently unhealthy") and apex-only, since the prober addresses
the container by IP, which the router treats as an unclaimed host.

Discovery builds that URL from the container's **first network IP and its
single exposed TCP port**. The Dockerfile's `EXPOSE 8080` is what supplies that
port -- image metadata, publishing nothing on the host, so it costs a deployment
nothing and needs no per-deployment label. Only one port may be declared or
discovery cannot choose; the admin port is deliberately left undeclared, and a
deployment that wants a different one sets `docker-updater.well-known.port`.

So no labels are needed at all. The caveat is the same one as the
port-only pre-check form below: the address is a bridge IP, so docker-updater
has to be on a network that reaches it -- it shares `internal` in
`deploy/docker-compose.yml`, which is enough. If a future deployment puts
buildhost on several networks and the updater on only one of them, discovery may
resolve an address it cannot reach; the full-URL label form below is the escape
hatch, and docker-updater says which of the two failed.

docker-updater reaches it over the **shared `internal` Docker network** via
buildhost's DNS alias, configured as a full URL:

```yaml
labels:
  docker-updater.pre-check.url: "http://buildhost-backend:8080/ready-to-update"
```

Do **not** use docker-updater's `:8080/...` (port-only) pre-check form: that one
resolves the container's bridge IP and therefore requires running docker-updater
with `--network host`. The full-URL form only needs docker-updater to share a
network with buildhost (it joins `internal` in `deploy/docker-compose.yml`), which
keeps the updater off host networking. Note that rolling updates (below) **skip**
pre-checks entirely -- the old container drains via graceful shutdown -- so the
pre-check endpoint matters only for non-rolling setups and the `try-update` CLI.

The `try-update` CLI subcommand wraps this endpoint for manual use or other
pre-update hooks:

```bash
buildhost try-update                    # queries localhost:8080/ready-to-update
buildhost try-update --addr :9090       # custom listen address
```

Exit 0 means idle (safe to update); non-zero means busy or unreachable (skip this
poll cycle).

The admin endpoint `GET /admin/inflight` on `:9090` still returns `{"inflight":
N}` with the raw count for dashboards.

Docker Compose label configuration for docker-updater with rolling updates:

```yaml
labels:
  docker-updater.enable: "true"
  docker-updater.rolling: "true"
stop_grace_period: 5m
```

## Docker image

The image is built from `gcr.io/distroless/static-debian12:nonroot`. It runs as
UID 65532 (nonroot) with no package manager. The server handles SIGTERM for
graceful shutdown.

The shipped binary is an Actually Portable Executable, so the image also carries
one static busybox as `/bin` plus a symlink per applet: the file's header is a
shell script, the image registers no binfmt handler, and the trampoline shells
out while it unpacks itself under `/tmp`.

**The entrypoint must never name the APE directly.** `/usr/local/lib/buildhost/
buildhost` is the APE and `/usr/local/bin/buildhost` is a `#!/bin/sh` launcher
that starts it -- the same shape the deb repackager gives an APE. A shebang
script is execable, so any spelling of the entrypoint works. This is not
cosmetic: a rolling updater creates the new container from the *old* container's
config, which carries the entrypoint resolved from the image that container was
created from. A container predating the APE carries `["buildhost"]`, and a bare
exec of an APE is ENOEXEC -- exit 126, on a loop, with the old container never
replaced and its stale config cloned onto every later image.

`dats/image-entrypoint.dats` guards the spelling, and `go-toolchain` runs it
sandboxed on every build. It reads the Dockerfile rather than starting a
container, because a bare exec cannot be reproduced from a shell at all: when
`execve` answers ENOEXEC the shell runs the file as a script instead, so the
broken form looks fine. Whether the launcher's target path is right is the
runtime question, and `container-healthcheck`'s `compose up --wait` answers it --
the launcher is the entrypoint, so a wrong path there is a container that never
starts.

The admin dashboard on `:9090` has **no built-in authentication**. It must be
placed behind a reverse proxy with access control (e.g., Cloudflare Access on a
separate hostname). Never expose port 9090 to untrusted networks.

Binary stripping needs no tools in the image: it is implemented natively in Go
(`internal/strip/elf.go`). This is a fix for a real production defect -- the image
ships no binutils, so the previous shell-out implementation silently stripped
NOTHING there for weeks. `container-healthcheck` now asserts against the built
image that a published ELF comes back stripped, that `fmt=symbols` serves its
debug info, and that `?debug=1` returns the upload byte-for-byte.

The server writes its own temporary files to `BUILDHOST_DATA_DIR/tmp`, not to
`/tmp`. `/tmp` still has to be writable, because the APE trampoline unpacks
itself there before the server starts. `read_only: true` therefore needs a
`tmpfs: [/tmp]` beside it as well as the data volume.
