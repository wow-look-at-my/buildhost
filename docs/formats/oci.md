# OCI distribution endpoint

`internal/oci/`. Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed.

The OCI distribution endpoint reads and writes on `oci.{domain}/v2/{project}/...`. `docker.{domain}` permanently redirects to `oci.{domain}`. GET and HEAD pull. POST, PATCH and PUT push, as `docker push` does.

## Auth discovery

The base endpoint `GET/HEAD /v2/` performs OCI auth discovery. It answers `401` with `WWW-Authenticate: Basic realm="buildhost"` for an unauthenticated request. It answers `200` only once a valid credential is in the request context, which the global auth middleware verifies. A `200` here makes a client conclude that no auth is needed. The client then never sends a credential, and the pull dies on the first manifest `401`.

## Pull side

The pull side synthesizes a minimal image from a binary artifact, in `internal/repackage/oci.go`. It serves a real pushed image instead when one exists.

The synthesized image has **two layers**, and three for an APE. The first is a shared, deterministic, memoized "essentials" base layer. It carries an embedded public CA bundle at `/etc/ssl/certs/ca-certificates.crt`, so outbound TLS works. It also carries `/etc/passwd` and `/etc/group` with root, nobody and nonroot, plus `/etc/nsswitch.conf` and a sticky `/tmp`. The per-binary layer follows it. The base layer is content-addressed, so it dedupes to one blob server-wide. It is registered per pull as an `oci-base-layer` packaged artifact, so the `BlobBelongsToProject` gate serves it.

Each per-platform **image manifest is likewise persisted and linked per pull**. `repackage.OCI.Repackage` stores it and calls `LinkOCIBlob` to record it in `oci_blob_links`. A multi-arch image index lists each platform's manifest by digest. Every child is therefore retrievable by `GET /v2/{project}/manifests/<digest>` and by `/blobs/<digest>`. `serveIndex` advertises only a child that resolves, so it never emits a dangling index.

`serveIndex` also persists the **top-level index document itself** under its own content digest. It uses `persistManifestBlob`, which is the same `Store.Put` plus `LinkOCIBlob` pair that `PutManifest` applies to a pushed manifest. The synthesized index is therefore retrievable by `GET/HEAD /v2/{project}/manifests/<index-digest>`, and not only by tag.

The Docker classic image store, which is the non-containerd overlay2 store, reads a manifest by tag. It then re-fetches the manifest by the advertised `Docker-Content-Digest`, to store it content-addressably. Without the persisted index that by-digest fetch answered 404. `docker pull <repo>:<tag>` then failed with `manifest unknown`, while a child platform pull still worked.

The config sets `Env`, which includes `SSL_CERT_FILE`. It also sets `WorkingDir`, the `/<project>` entrypoint, and `User`. `User` comes from the release's optional `oci_user` field. An empty value means root.

### An APE image

The kernel cannot exec an Actually Portable Executable. The file's header is a shell script, and the image registers no binfmt handler. `peekAPE` reads the first eight bytes of the artifact and looks for the APE prologue. buildhost never runs an upload to learn what it is.

A linux APE therefore gets a **third layer**, from `ShellCache`. That layer carries one static busybox at `/bin/busybox`, plus a symlink per applet. The trampoline shells out while it unpacks itself under `/tmp`. The layer comes from a pinned `busybox:musl` image. Its digest is checked against that pin. The layer is deterministic, so its diffID is the same on every server. It is registered per pull as an `oci-shell-layer` packaged artifact.

**The entrypoint must never name the APE.** The binary goes to `/usr/local/lib/<project>/<project>`. A `#!/bin/sh` launcher takes its place at `/<project>`. A copy sits at `/usr/local/bin/<project>` for the bare name on PATH. The entrypoint stays `/<project>`, which is what every earlier synthesized image carried. This is the shape the Dockerfile and the deb repackager already give an APE.

This is not cosmetic. A rolling updater creates the replacement container from the config of the container it replaces. That config carries the entrypoint of the OLD image. An entrypoint that names an APE exits 126 on every start. The old container is never replaced. Its stale config is then cloned onto every later image. The deployment stays on the version it already runs. A shebang script is execable, so every spelling reaches the binary and no such wedge can start.

`test/dats/synthesized-ape-image.dats` starts a real synthesized APE image. It also starts one under each entrypoint spelling an older container can carry.

## Push side

The push side is `push.go`, `upload.go` and `putmanifest.go`. It accepts a monolithic or chunked blob upload, streamed to `DataDir/tmp/oci-uploads`. It accepts a manifest PUT and an index PUT. It records a `kind=docker` artifact.

The chunked upload session is **resumable**. PATCH verifies an optional `Content-Range` start against the committed size. A mismatch answers 416 with the current `Range`, and consumes nothing. A client that lost a response therefore cannot corrupt the blob by a re-send. `GET /v2/{name}/blobs/uploads/{uuid}` reports the committed `Range` in a 204, for the resume. Session sweeping goes by **last activity**, at 2h idle, and not by creation time. A long chunked upload therefore never dies mid-flight.

The route's `Access()` is method-aware. A push verb needs write. Every `uploads`-action route requires write whatever the method, because the GET status read is push-flow state. The package self-registers through init().

### Cross-repository mount

`POST /v2/{name}/blobs/uploads/?mount=<digest>[&from=<project>]` links a blob that storage already holds, instead of receiving it again. It answers 201 when it grants the mount.

Storage is content-addressed and server-wide. The bytes are therefore present whoever pushed them first. The mount decides only whether this project may point at them. It may when the caller can READ a project that already links the blob. `auth.TokenCanReadProject` decides that over `DB.ListOCIBlobOwners`. The mount then discloses nothing that a pull does not.

In every other case, and when storage no longer holds the bytes, the request falls through to an ordinary upload session with a 202. That is the specification's fallback. It is always correct, and only slower. `from` narrows the search to one project rather than widening it.

Without this, every image built `FROM` a published base re-uploads that base into its own project. A fan-out of harness images on one session image re-sent several hundred megabytes each, in parallel. That redundant load is what turned a single registry hiccup into a set of failed publishes. The client asks to mount every blob before it uploads it (`ociclient.Pusher.startSession`). No caller therefore has to know where a base came from.

### When the registry forgets a session

A session lives in server memory (`uploadStore`). A restart therefore takes every one of them. A later request then answers `BLOB_UPLOAD_UNKNOWN`. There is nothing to resume from. `ociclient` opens a fresh session and re-sends the blob from zero, rather than fail a publish that is minutes deep. For the same reason it retries the opening of a session on a 5xx.

## Published layers are zstd, with no opt-out

`buildhost-publish-docker` exports `compression=zstd,force-compression=true`. There is no input that selects an algorithm. The synthesized images have always been zstd. The gzip default in buildx was the only reason a published image differed from a synthesized one. `force-compression` is what reaches a layer that arrives already compressed, from a cache hit or a `FROM` base. An image therefore cannot ship half gzip. Only `compression-level`, zstd 0 to 22, stays adjustable.

A consumer therefore needs an OCI-aware puller. Docker's containerd image store qualifies. It is the default on a fresh Engine 29.0 or later install, and `features.containerd-snapshotter` selects it before that. containerd, podman and go-containerregistry qualify too. Docker's classic image store cannot read a zstd layer at all. The failure is a hard "media type application/vnd.oci.image.layer.v1.tar+zstd not supported" on pull.

Every push is then pulled back. Its recorded `org.opencontainers.image.revision` is checked against the building commit. That check runs in the action rather than in a caller's workflow. The action knows the refs it pushed. A registry that stored the wrong bytes looks identical from the build host.

## Docker push as a release kind

A release that contains a pushed `kind=docker` artifact is a "docker build". The OCI endpoint is the only place it is served. `kind=docker` is gated out of apt, brew, npm, and the raw `/static` and `/dl` paths.

A pushed blob or manifest is linked to the project in `oci_blob_links`, so the existing `BlobBelongsToProject` pull gate serves it. A pushed tag lives in `oci_tags` as a mutable pointer. `latest` is an alias. A digest is immutable. An identical re-push is a no-op. A changed image creates a new auto-versioned release and repoints the tag.

`docker login` uses Basic auth against the token system. A GHA OIDC JWT works as the password, and it auto-provisions the project.

Behind a body-capping proxy, `buildhost docker-push` (internal/ociclient) is the working push path for a layer over the cap. The docker and buildx clients send each blob as one request. They die on the proxy's 413.
