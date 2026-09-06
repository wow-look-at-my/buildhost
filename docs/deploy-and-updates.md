# Graceful shutdown and update coordination

Extracted verbatim from CLAUDE.md, no wording changed.

The server handles SIGTERM/SIGINT by calling `http.Server.Shutdown` with a 5-minute timeout, allowing in-flight requests (especially large uploads) to complete before the process exits.

For a zero-downtime update, use docker-updater's rolling update mode with an nginx sidecar. The label is `docker-updater.rolling: "true"`. docker-updater starts the new container before it stops the old one. nginx routes through Docker DNS. See `deploy/` for an example compose stack.

**Two versions must never share the service alias.** `deploy/nginx.conf` resolves the `buildhost-backend` alias per request. Docker's embedded DNS returns every address an alias resolves to, and it rotates them. Any container that holds that alias is therefore taking live traffic.

docker-updater used to copy the alias onto the replacement at CREATE time. That load-balanced clients across the old and the new IMAGE for the whole health wait. This image sets `HEALTHCHECK --interval=30s --start-period=5s`, so the window ran from 5 to 35 seconds. About half of all requests in it were answered by the build under replacement. That is what made a publish come back missing the `artifacts` field its own release had just added.

docker-updater now withholds the aliases until the replacement is healthy. It then moves them and stops the old container. Exactly one version therefore serves a request. Nothing in this repo compensates for version skew any more. The publish action fails on a missing field rather than a retry.

## Ready-to-update endpoint

`GET /ready-to-update` on the main server (`:8080`) returns HTTP 200 when the server is idle, or HTTP 503 when there are in-flight write requests. It is designed for docker-updater's HTTP pre-update checks -- no exec into the container (the distroless image has no shell).

### The standard `/.well-known/docker-updater/` spellings

The same two handlers also answer at the paths docker-updater discovers by itself, with no label at all:

| Path | Same as | Answers |
|---|---|---|
| `/.well-known/docker-updater/health` | `/healthz` | 200 while the database pings, 503 otherwise |
| `/.well-known/docker-updater/pre-update` | `/ready-to-update` | 200 when idle, 503 with writes in flight |

They are aliases on purpose. A second implementation of "is it healthy" is a second answer that can disagree with the first. Both are registered with `HandleRaw`, so they carry no project auth. The prober carries no credential. A 401 reads as "serving but permanently unhealthy". Both are apex-only, because the prober addresses the container by IP. The router treats such a host as unclaimed.

Discovery builds that URL from the container's **first network IP and its single exposed TCP port**. The Dockerfile's `EXPOSE 8080` supplies that port. It is image metadata and it publishes nothing on the host. It therefore costs a deployment nothing. It needs no per-deployment label. Only one port may be declared, or discovery cannot choose. The admin port is therefore left undeclared. A deployment that needs a different port sets `docker-updater.well-known.port`.

No labels are needed at all. The caveat is the same as the one on the port-only pre-check form below. The address is a bridge IP, so docker-updater must sit on a network that reaches it. It shares `internal` in `deploy/docker-compose.yml`, which is enough.

A future deployment may put buildhost on several networks and the updater on only one of them. Discovery can then resolve an address it cannot reach. The full-URL label form below is the escape hatch. docker-updater says which of the two failed.

docker-updater reaches it over the **shared `internal` Docker network** via buildhost's DNS alias, configured as a full URL:

```yaml
labels:
  docker-updater.pre-check.url: "http://buildhost-backend:8080/ready-to-update"
```

Do **not** use docker-updater's `:8080/...` port-only pre-check form. That form resolves the container's bridge IP. It therefore needs docker-updater to run with `--network host`. The full-URL form needs only a shared network with buildhost, which it joins as `internal` in `deploy/docker-compose.yml`. That keeps the updater off host networking.

A rolling update, described below, **skips** a pre-check entirely. The old container drains through graceful shutdown instead. The pre-check endpoint therefore matters only for a non-rolling setup and for the `try-update` CLI.

The `try-update` CLI subcommand wraps this endpoint for manual use or other pre-update hooks:

```bash
buildhost try-update                    # queries localhost:8080/ready-to-update
buildhost try-update --addr :9090       # custom listen address
```

Exit 0 means idle. An update is then safe. A non-zero exit means busy or unreachable. The caller must then skip this poll cycle.

The admin endpoint `GET /admin/inflight` on `:9090` still returns `{"inflight": N}` with the raw count for dashboards.

Docker Compose label configuration for docker-updater with rolling updates:

```yaml
labels:
  docker-updater.enable: "true"
  docker-updater.rolling: "true"
stop_grace_period: 5m
```

## Docker image

The image is built from `gcr.io/distroless/static-debian12:nonroot`. It runs as UID 65532 (nonroot) with no package manager. The server handles SIGTERM for graceful shutdown.

The shipped binary is an Actually Portable Executable. The image therefore also carries a static busybox as `/bin`, plus a symlink per applet. The file's header is a shell script. The image registers no binfmt handler. The trampoline shells out while it unpacks itself under `/tmp`.

**The entrypoint must never name the APE directly.** `/usr/local/lib/buildhost/buildhost` is the APE. `/usr/local/bin/buildhost` is a `#!/bin/sh` launcher that starts it. That is the same shape the deb repackager gives an APE. A shebang script is execable, so any spelling of the entrypoint works.

This is not cosmetic. A rolling updater creates the new container from the *old* container's config. That config carries the entrypoint resolved from the image the old container came from. A container that predates the APE carries `["buildhost"]`. A bare exec of an APE is ENOEXEC, which means exit 126, on a loop. The old container is never replaced, and its stale config is cloned onto every later image. Docker reports the failure against the entrypoint path. The message therefore names a file that is present.

`dats/image-entrypoint.dats` guards the spelling. `go-toolchain` runs it sandboxed on every build. It reads the Dockerfile rather than starting a container, because a bare exec cannot be reproduced from a shell at all. When `execve` answers ENOEXEC the shell runs the file as a script instead, so the broken form looks fine. It covers both spellings: the entrypoint must name the launcher, and the shell must come from an image that ships a static busybox.

`test/dats/image-entrypoints.dats` is the runtime half, and `container-healthcheck` runs it. It starts the built image once per spelling an old container can carry: the bare name on PATH, the absolute launcher path, and a shell in front of the path. It also asserts that nothing the image ships names an ELF interpreter, because the base image has no `/lib` to load one from.

A bare exec IS reproducible. `docker run --entrypoint buildhost` on an image whose entrypoint path is the APE fails, and docker reports it against that path. It reproduces only where no APE binfmt handler is registered. The handler is host-wide, and containers inherit it. go-toolchain registers one on the runners it uses. With one registered, the kernel runs any APE through a shell. These assertions then cannot fail. The suite refuses to run when it finds one.

`dats/image-entrypoint.dats` guards the spelling. `go-toolchain` runs it sandboxed on every build. It reads the Dockerfile rather than starting a container, because a bare exec cannot be reproduced from a shell at all. When `execve` answers ENOEXEC the shell runs the file as a script instead, so the broken form looks fine.

The runtime question is whether the launcher's target path is right. `container-healthcheck` answers it with `compose up --wait`. The launcher is the entrypoint, so a wrong path there is a container that never starts.

The admin dashboard on `:9090` has **no built-in authentication**. It must be placed behind a reverse proxy with access control (e.g., Cloudflare Access on a separate hostname). Never expose port 9090 to untrusted networks.

Binary stripping needs no tools in the image. It is implemented natively in Go (`internal/strip/elf.go`). This is a fix for a real production defect. The image ships no binutils, so the previous shell-out implementation silently stripped NOTHING there. `container-healthcheck` now asserts against the built image that a published ELF comes back stripped. It also asserts that `fmt=symbols` serves the debug info, and that `?debug=1` returns the upload byte-for-byte.

The server writes its own temporary files to `BUILDHOST_DATA_DIR/tmp`, not to `/tmp`. `/tmp` still has to be writable, because the APE trampoline unpacks itself there before the server starts. `read_only: true` therefore needs a `tmpfs: [/tmp]` beside it as well as the data volume.
