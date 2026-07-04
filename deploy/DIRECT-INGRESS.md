# Direct TLS ingress (any-size uploads, no Cloudflare proxy)

## Why this exists

When buildhost is served through Cloudflare's proxy (for example via the
`cloudflared` tunnel in this compose file, with proxied/orange-cloud DNS
records), Cloudflare's edge enforces a hard **request-body cap on proxied
hostnames** (100 MB on the Free plan) -- any artifact upload larger than
that dies at the edge with `413 Payload Too Large` before it ever reaches
buildhost.

This directory ships an **opt-in** second ingress that bypasses the proxy
for one hostname: a Caddy reverse proxy (`ingress` service in
`docker-compose.yml`, image built by `Dockerfile.ingress`, config in
`Caddyfile.ingress`) that terminates TLS itself on port **8443** and streams
requests straight to the same `buildhost-backend:8080` upstream the existing
nginx sidecar uses. Nothing about the existing proxied path changes; without
the `direct-ingress` compose profile the stack is byte-for-byte what it was.

- **Uploads of any size, one plain HTTP request.** Caddy has no request-body
  limit and never buffers request bodies; the only remaining cap is the
  app's own `BUILDHOST_MAX_UPLOAD_SIZE` (default 2 GiB -- raise it in the
  `buildhost` service environment, e.g. `BUILDHOST_MAX_UPLOAD_SIZE=20G`).
  No read/write timeouts are configured, so multi-hour uploads survive.
- **TLS terminates in the sidecar** because buildhost itself has no
  cert/ACME support (plain `ListenAndServe` behind a reverse proxy, by
  design).
- **Certificates come from ACME DNS-01** (Let's Encrypt) via the Cloudflare
  DNS API. Use DNS-01 when ports 80/443 aren't available to this stack for
  ACME challenges (HTTP-01 and TLS-ALPN-01 both need one of them); it needs
  no inbound connectivity at all to issue. Issued certs and the ACME
  account persist in the `ingress-data` volume, so restarts never re-issue;
  Caddy renews automatically in the background.
- **No app changes needed.** buildhost is host-agnostic: it answers on any
  `Host` header and derives generated URLs from the request Host, so a
  dedicated upload hostname (e.g. `api.pazer.build:8443`) works with zero
  code or config changes.

## What still goes through Cloudflare

Everything else. Downloads, the web UI, apt/brew/npm/oci/dl/static/sites --
all keep using the proxied hostnames and stay cached and shielded by
Cloudflare. This ingress exists for **uploads** (and any other large
request bodies). Upload URLs are simply the same API paths on the direct
hostname:

```
PUT https://api.pazer.build:8443/api/v1/projects/{project}/releases/latest/artifacts/{os}/{arch}
```

or point the CLI at it: `buildhost publish --server https://api.pazer.build:8443 ...`

The direct ingress proxies only the API backend (`:8080`). The admin
dashboard (`:9090`, unauthenticated) is **not** exposed through it.

## Operator setup

One-time steps, in order. Examples below use `api.pazer.build` as the
ingress hostname -- substitute your own (`INGRESS_HOST`).

1. **DNS record** -- in the Cloudflare dashboard for your zone, create an
   A record: `<your-ingress-hostname> -> <your-WAN-IP>` with the proxy
   toggled **off** (DNS only / grey cloud). An explicit DNS-only record
   overrides a proxied wildcard for exactly that one name; every other
   hostname stays proxied.

2. **Port-forward** -- forward an external TCP port (default **8443**) to
   port **8443** on the host running this compose stack.

3. **Cloudflare API token** -- create a token scoped to exactly
   **Zone / DNS / Edit** for your zone (My Profile -> API Tokens -> Create
   Token -> "Edit zone DNS" template). Put it in the deploy `.env` next to
   `docker-compose.yml`:

   ```
   CLOUDFLARE_DNS_API_TOKEN=<the token>
   INGRESS_ACME_EMAIL=<you@example.com>   # optional: ACME expiry notices
   ```

4. **Start it** -- the service sits behind a compose profile so existing
   deploys are untouched until you opt in:

   ```bash
   docker compose --profile direct-ingress up -d --build
   ```

   First start builds the Caddy image (xcaddy compiles in the
   `caddy-dns/cloudflare` module) and obtains the certificate; watch with
   `docker compose logs -f ingress` until you see
   `certificate obtained successfully`.

5. **Verify:**

   ```bash
   # TLS + end-to-end reachability (no auth needed):
   curl -sS https://api.pazer.build:8443/healthz

   # A >100 MB upload that would 413 at Cloudflare's edge:
   dd if=/dev/urandom of=/tmp/big.bin bs=1M count=200
   curl -fSs -H "Authorization: Bearer $TOKEN" -T /tmp/big.bin \
     "https://api.pazer.build:8443/api/v1/projects/upload-test/releases/latest/artifacts/linux/amd64"
   ```

## Configuration

All knobs are compose environment variables with working defaults:

| Variable | Default | Meaning |
|----------|---------|---------|
| `CLOUDFLARE_DNS_API_TOKEN` | (required) | Cloudflare token, Zone / DNS / Edit on the ingress hostname's zone, used for ACME DNS-01 |
| `INGRESS_HOST` | `api.pazer.build` | Hostname to obtain a cert for and pin TLS/SNI to (handshakes for any other name are refused) |
| `INGRESS_PORT` | `8443` | Published host port (the container side is fixed at 8443) |
| `INGRESS_UPSTREAM` | `buildhost-backend:8080` | Proxy upstream |
| `INGRESS_ACME_EMAIL` | `hostmaster@pazer.build` | ACME account email (cert expiry notices) |

Rolling updates keep working: like the nginx sidecar, Caddy re-resolves the
upstream hostname per dial (Docker DNS), and retries dial failures for a few
seconds while docker-updater swaps the buildhost container.
