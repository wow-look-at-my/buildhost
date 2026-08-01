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
UID 65532 (nonroot) with no shell, no package manager, and no writable paths
except the data volume. The server handles SIGTERM for graceful shutdown.

The admin dashboard on `:9090` has **no built-in authentication**. It must be
placed behind a reverse proxy with access control (e.g., Cloudflare Access on a
separate hostname). Never expose port 9090 to untrusted networks.

Binary stripping needs no tools in the image: it is implemented natively in Go
(`internal/strip/elf.go`). This is a fix for a real production defect -- the image
ships no binutils, so the previous shell-out implementation silently stripped
NOTHING there for weeks. `container-healthcheck` now asserts against the built
image that a published ELF comes back stripped, that `fmt=symbols` serves its
debug info, and that `?debug=1` returns the upload byte-for-byte.

All temporary files are written to `BUILDHOST_DATA_DIR/tmp`, not to `/tmp`. The
image is compatible with `read_only: true` as long as the data volume is mounted.
