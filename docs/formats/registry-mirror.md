# Registry mirror

`internal/mirror/`. A read-only pull-through mirror of public registries. Each upstream has its own subdomain, because a docker daemon's `registry-mirrors` entry cannot carry a path.

| Subdomain | Upstream |
| --- | --- |
| `dockerhub.{domain}` | `https://registry-1.docker.io` |
| `ghcr.{domain}` | `https://ghcr.io` |

## What it serves

`GET` and `HEAD` on `/v2/`, `/v2/<repository>/manifests/<tag or digest>` and `/v2/<repository>/blobs/<digest>`. Every other method answers 405. A job can never change what a later job pulls. A image of the Docker Hub official keeps its `library/` prefix, which is what a daemon sends to a mirror.

## Auth

Every request needs a buildhost token with the `read` scope, sent as Bearer or as the Basic password. `/v2/` answers 401 with a Basic challenge without one. An open mirror spends the server's bandwidth and its upstream quota for anyone who finds it. The mirror fetches from each upstream anonymously. It holds only public content.

A docker daemon sends no credential to an entry in `registry-mirrors`. A client that must reach the mirror directly puts a local proxy. In front that adds the token. webhook-runner does this for its dind containers and the host daemon.

## Storage

A manifest or a blob comes from the upstream once. It is stored in the blob store under its digest, and served from there after that, with `Range` support. A blob streams to the first client while it is written to a temporary file. A large layer starts to flow at once. The file goes into the store only when its SHA-256 matches the digest it was asked for. A client that disconnects does not stop the fetch, so the next pull finds the blob.

The rows are `mirror_blobs` (content) and `mirror_tags` (the digest a tag named). They are not projects or releases and never show on a package surface. `IsBlobReferenced` and the retention inventory count `mirror_blobs`, so the release GC never sweeps them.

## Tags

A tag is resolved with a `HEAD` to the upstream, at most once per `mirror.TagTTL` (one minute). Docker Hub does not count a `HEAD` against its pull limit. When the upstream fails, the digest it named last still serves, and the server logs a warning. An upstream that says the image is absent is a 404. Every other upstream failure is a 502 that names the upstream and its answer, not a 404. A 429 from the upstream is relayed as 429.

A tag that names a media type the request's `Accept` header leaves out answers 404 with the type in the message.

## Eviction

The mirror janitor runs every few minutes. It deletes a blob nothing read for `BUILDHOST_MIRROR_KEEP` (default `168h`), with the tags that name it. While free disk is below `BUILDHOST_MIRROR_MIN_FREE_PERCENT` (default 15), it deletes the least recently used blobs first. A malformed value of either fails startup.

## Dashboard

The admin dashboard's Registry Mirror page (`#/mirror`, `GET /api/mirror`) lists every tag and stored blob, the disk share, and the last janitor pass.
