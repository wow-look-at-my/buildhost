# OCI distribution endpoint

`internal/oci/`. Extracted verbatim from CLAUDE.md; paragraph breaks were added
at the existing topic boundaries, no wording changed.

OCI distribution endpoint (read + write) on `oci.{domain}/v2/{project}/...`.
`docker.{domain}` permanently redirects to `oci.{domain}`. GET/HEAD pulls;
POST/PATCH/PUT pushes (`docker push`).

## Auth discovery

The base endpoint `GET/HEAD /v2/` performs OCI auth discovery: it answers `401`
with `WWW-Authenticate: Basic realm="buildhost"` when unauthenticated and `200`
only once a valid credential is in the request context (the global auth
middleware verifies it) -- a `200` here would make clients conclude no auth is
needed and never send credentials, killing the pull on the first manifest `401`.

## Pull side

Pull side synthesizes a minimal image from a binary artifact (in
`internal/repackage/oci.go`) OR serves a real pushed image. The synthesized image
has **two layers**: a shared, deterministic, memoized "essentials" base layer (an
embedded public CA bundle at `/etc/ssl/certs/ca-certificates.crt` so outbound TLS
works, plus `/etc/passwd`+`/etc/group` with root/nobody/nonroot,
`/etc/nsswitch.conf`, and a sticky `/tmp`) followed by the per-binary layer; the
base layer is content-addressed (deduped to one blob server-wide) and registered
per-pull as an `oci-base-layer` packaged artifact so the `BlobBelongsToProject`
gate serves it.

Each per-platform **image manifest is likewise persisted and linked per-pull**
(`repackage.OCI.Repackage` stores it and `LinkOCIBlob`s it into `oci_blob_links`),
so a multi-arch image index -- which lists each platform's manifest by digest --
has every child retrievable by `GET /v2/{project}/manifests/<digest>` (and
`/blobs/<digest>`); `serveIndex` advertises only children that resolve, so it
never emits a dangling index.

`serveIndex` also persists the **top-level index document itself** under its own
content digest (via `persistManifestBlob` -- the same `Store.Put`+`LinkOCIBlob`
`PutManifest` applies to pushed manifests), so the synthesized index is
retrievable by `GET/HEAD /v2/{project}/manifests/<index-digest>`, not only by tag.
The Docker classic (non-containerd / overlay2) image store reads a manifest by
tag, then re-fetches it by the advertised `Docker-Content-Digest` to store it
content-addressably; without the persisted index that by-digest fetch 404'd and
`docker pull <repo>:<tag>` failed with `manifest unknown` even though child
platform pulls worked.

Config sets `Env` (incl. `SSL_CERT_FILE`), `WorkingDir`, the `/<project>`
entrypoint, and `User` from the release's optional `oci_user` field (empty =
root).

## Push side

Push side (`push.go`, `upload.go`, `putmanifest.go`) accepts blob uploads
(monolithic + chunked, streamed to `DataDir/tmp/oci-uploads`) and manifest/index
PUTs, recording `kind=docker` artifacts. The chunked upload session is
**resumable**: PATCH verifies an optional `Content-Range` start against the
committed size (mismatch = 416 + current `Range`, nothing consumed -- so a client
that lost a response can't corrupt the blob by re-sending), `GET
/v2/{name}/blobs/uploads/{uuid}` reports the committed `Range` (204) for resume,
and session sweeping goes by **last activity** (2h idle), not creation time, so a
long chunked upload never dies mid-flight. Route `Access()` is method-aware (write
for push verbs), and every `uploads`-action route requires write regardless of
method (the GET status read is push-flow state). Self-registering via
auth.OnReady().

### Cross-repository mount

`POST /v2/{name}/blobs/uploads/?mount=<digest>[&from=<project>]` links a blob
storage already holds instead of receiving it again, answering 201 when granted.
Storage is content-addressed and server-wide, so the bytes are there whoever
pushed them first; what the mount decides is only whether this project may point
at them. It may when the caller can READ a project that already links the blob
(`auth.TokenCanReadProject` over `DB.ListOCIBlobOwners`) -- then the mount
discloses nothing a pull would not. Otherwise, and when storage no longer has the
bytes, the request falls through to an ordinary upload session (202), which is
the spec's fallback and always correct, just slower. `from` narrows the search to
one project rather than widening it.

Without this every image built `FROM` a published base re-uploads that base into
its own project: a fan-out of six harness images on one session image re-sent
several hundred megabytes each, in parallel, and the redundant load is what
turned a single registry hiccup into four failed publishes. The client asks to
mount every blob before uploading it (`ociclient.Pusher.startSession`), so no
caller has to know where a base came from.

### When the registry forgets a session

Sessions are server memory (`uploadStore`), so a restart takes every one of them
and later requests answer `BLOB_UPLOAD_UNKNOWN`. There is nothing to resume from,
so `ociclient` opens a fresh session and re-sends the blob from zero rather than
failing a publish that is minutes deep; opening a session is retried on 5xx for
the same reason.

## Docker push as a release kind

A release containing pushed `kind=docker` artifacts is a "docker build" -- served
only via the OCI endpoint. `kind=docker` is gated out of apt/brew/npm and the raw
`/static` (+ `/dl`) paths. Pushed blobs/manifests are linked to the project in
`oci_blob_links` (so the existing `BlobBelongsToProject` pull gate serves them);
pushed tags live in `oci_tags` as mutable pointers (`latest` is an alias, digests
are immutable, identical re-push is a no-op, a changed image creates a new
auto-versioned release and repoints the tag). `docker login` uses Basic auth ->
the token system; a GHA OIDC JWT works as the password and auto-provisions the
project. Behind a body-capping proxy, `buildhost docker-push`
(internal/ociclient) is the working push path for >cap layers -- docker/buildx
send each blob as one request and die on the proxy's 413.
