# Uploads: chunked sessions, fan-out and hash references

`internal/uploads/`, `internal/uploadclient/`, `internal/ociclient/`, and the
artifact-upload half of `internal/api/`. Extracted verbatim from CLAUDE.md;
paragraph breaks were added at the existing topic boundaries, no wording changed.

## Multi-platform fan-out

The artifact upload (`PUT .../artifacts/{os}/{arch}`) supports **multi-platform
fan-out** (`expandOSSpec`/`expandArchSpec` in `artifacts.go`): `{os}` takes a
single OS, a comma list, or the alias `cosmo`/`any`/`all`/`universal` (->
linux+darwin+windows, for Cosmopolitan/APE binaries that run everywhere); `{arch}`
takes a list or `any`/`all` (-> amd64+arm64). Elements are normalized via
`db.NormalizeOS`/`db.NormalizeArch` (upload accepts the same alias spellings as
download); invalid/empty/duplicate elements 400. The body streams to storage ONCE
and each os x arch combination becomes an ordinary artifact row sharing that blob
-- rows are created all-or-nothing (`db.CreateArtifacts`, one transaction; any
conflicting combination 409s naming it, nothing created), so the read path
(dl/static/formats/retention) is completely unchanged. A single canonical os/arch
keeps the byte-identical single-object response; multi returns a JSON array of the
same artifact objects. `kind=npm-package` keeps its literal os=any/arch=any
sentinel row (never fans out).

Multi-platform publish fan-out happens at upload time, never at download time: one
uploaded blob -> N ordinary per-platform artifact rows (comma list or
`cosmo`/`any` alias in the upload URL's `{os}`/`{arch}` segments). There is
deliberately no stored `os=any` value and no download-time fallback, so downloads,
`latest` resolution, format handlers, retention refcounting (`IsBlobReferenced`
counts the shared blob until the last row goes), and CDN caching all see plain
per-platform artifacts.

## Hash-reference uploads

The same PUT also accepts a **hash-reference upload**: an EMPTY body
(`ContentLength == 0`) with `?upload_sha256=<64-hex>` and NO `upload_session`
registers row(s) for a blob the project already has in storage instead of
re-sending bytes -- how one uploaded binary covers an exact slot set the cartesian
grammar cannot express (e.g. {linux/amd64, linux/arm64, windows/amd64} with
windows/arm64 left free), and how unchanged re-releases skip the transfer.

Flow (`resolveHashRef` in artifacts.go): hex-shape 400 -> same-project gate
`db.BlobBelongsToProject` (failure is a 404 byte-identical to an unknown blob, so
a public sha256 can never mint a row serving another project's bytes) ->
`Store.Exists` (404 when GC'd) -> header-only `Store.Get` for the row size; the
combination build, row insert, 409 conflict semantics, and 201 responses are
shared with full uploads, and each hash-ref request carries its own
`X-Artifact-Filename` (per-slot filenames, unlike fan-out's single shared header).

Server-info advertises `upload_by_sha256: true`. Body-carrying PUTs (param
ignored) and session finalizes (param = spool integrity check) are byte-identical
to before.

**Fan-out first, hash references only for what fan-out cannot express.** A
publisher holding one file that belongs in several slots reaches for the URL
grammar before it reaches for hashes: `PUT .../artifacts/linux,darwin,windows/amd64,arm64`
streams the body ONCE. The buildhost-publish action groups discovered artifacts by
sha256 and, when a group's slots are exactly the product of the platforms it
covers -- which is every cosmo build, the case that motivated hash references at
all -- sends that one request. Only a ragged set (`{linux/amd64, linux/arm64,
windows/amd64}`, no windows/arm64) falls through to upload-first-then-hash-ref,
with a full upload on any non-201/409 in case the blob was evicted in between.
`buildhost publish --manifest` still hash-refs per entry: a manifest names each
slot's own `X-Artifact-Filename`, which fan-out's single shared header cannot
carry.

The action does NOT probe server-info for the capability first. There is one
buildhost, it advertises `upload_by_sha256`, and a dedicated round-trip to
re-confirm a constant is a probe for a known truth. `internal/uploadclient` still
exposes `SupportsUploadBySHA256()` for the CLI, where server-info is already
fetched for the chunk threshold so the answer is free.

## Chunked upload sessions (internal/uploads)

Generic chunked upload sessions, so a client can deliver an arbitrarily large
request body to ANY existing upload endpoint in pieces small enough to survive a
proxy's request-body cap (Cloudflare's edge rejects bodies over 100 MB with a 413
that never reaches the origin). `POST /api/v1/uploads` creates a session (any
`write`-scoped credential; bound to the creator's identity -- token ID + name,
covering both DB tokens and OIDC synthetic tokens); `PATCH
/api/v1/uploads/{id}?offset=N` appends a chunk with strict offset verification
(409 + committed size on mismatch, so uploads resume; partially transferred chunk
bytes are committed); `GET` reads the size; `DELETE` aborts.

**Finalize by reference** is the key move: `ResolveSessionBody` middleware (wired
in `internal/server` between `Authenticate` and routing) intercepts any mutating
request carrying `?upload_session=<id>` with an empty body, verifies ownership
(404 otherwise -- no existence leak) and the optional
`?upload_sha256=`/`X-Upload-SHA256` integrity hash, then swaps the spool file in
as `r.Body` -- so every existing endpoint's routing, project auth, size caps, and
storage logic runs unchanged. The session is consumed on a 2xx response, kept for
a retry otherwise. Spools live at `{DataDir}/tmp/uploads/<id>.spool`, are capped at
`BUILDHOST_MAX_UPLOAD_SIZE` **at append time**, and are swept after
`BUILDHOST_UPLOAD_SESSION_TTL` (default 24h; opportunistic sweep on create + a
15-minute janitor started by serve + orphan cleanup at startup). Sessions are
in-memory + spool file (same model as the OCI blob upload store): a container swap
mid-session 404s the next chunk cleanly and the client restarts.

The server sits behind Cloudflare (edge-413s request bodies over 100 MB), so `GET
/api/v1/server-info` (public) advertises `max_direct_upload_bytes`
(`BUILDHOST_MAX_DIRECT_UPLOAD_SIZE`, default 95 MiB) and anything larger goes
through a chunked upload session: assemble with `POST /api/v1/uploads` + `PATCH
.../{id}?offset=N`, then call the ORIGINAL upload endpoint with an empty body and
`?upload_session=<id>` (+ optional `?upload_sha256=`) -- middleware swaps the spool
in as the request body, reusing all existing endpoint logic. The CLI does this
automatically; clients decide from the advertised limit BEFORE sending, never by
reacting to a 413. The total assembled size is still capped by
`BUILDHOST_MAX_UPLOAD_SIZE` at append time.

## The CLI upload engine (internal/uploadclient)

The CLI's upload engine (used by `publish` and `publish-site`): stats the file
locally, fetches the server's advertised `max_direct_upload_bytes` from `GET
/api/v1/server-info` (fallback: built-in 95 MiB), and picks direct-vs-chunked
BEFORE sending anything -- the first attempt is the one that succeeds; there is no
try-a-big-PUT-and-react-to-413 path. Small files keep the classic single request
byte-for-byte; larger ones create a session, append sequential chunks (default 64
MiB, `--chunk-size`, `0` disables) with per-chunk retry/backoff that resumes from
the server's committed size (409 or status read), then finalize with the file's
sha256 and best-effort DELETE the session on hard failure. A missing session
endpoint is a hard error, not a mode: the one buildhost advertises
`upload_sessions`, and the old 404/405 fallback's real effect was to send a
several-hundred-megabyte single request for the proxy to reject with a 413 nobody
could trace back to the client. Lives in `internal/` (not `cmd/`) so its
tests don't drag the untested CLI package into coverage.

## The docker-push engine (internal/ociclient)

The CLI's `docker-push` engine: pushes a locally built OCI image layout (`docker
buildx build --output type=oci` tarball or `tar=false` directory, or `docker
save`) to the OCI endpoint, uploading every blob larger than the server's
advertised `max_direct_upload_bytes` through the OCI chunked upload session
(sequential `Content-Range` PATCH appends each under the limit, digest-checked PUT
finalize) -- so image layers over a fronting proxy's request-body cap (Cloudflare
edge 413s ~100 MB) publish where docker/buildx/crane's one-request-per-blob
uploads cannot.

Sizing is decided up front from the blob size + server-info (never by reacting to
a 413); `--chunk-size` only clamps DOWN from the advertised limit; interrupted
chunks resume from the server's committed size (416 `Range` / the status GET).
Blobs the registry already has are HEAD-skipped; small blobs keep the classic
monolithic `POST ?digest=`. The layout walk pushes depth-first (blobs, then child
manifests by digest -- attestation manifests included -- then the root by each tag)
and requires exactly one top-level `index.json` entry (one image per push).

The `buildhost-publish-docker` action builds with buildx `--output
type=oci,tar=false` and pushes through this client (building the CLI from the
action's own checkout); tags referencing foreign registries still go through
buildx `push: true`. When its `tags` input is omitted the action computes
branch-aware defaults (TypeScript step `id: default-tags` in the action): the
commit SHA + the docker-sanitized short ref name (lowercase; runs of
`[^a-z0-9._-]` -> `-`; leading dots/dashes stripped -- so a `claude/foo` branch
yields the bare buildhost-bound tag `claude-foo`, never a foreign `/`-reference),
plus `latest` ONLY when the push is to the repo's default branch (read from the
event payload; payload-less events like `schedule` fall back to `master`, failing
closed -- no `latest` -- on `main`-defaulted repos). Feature-branch pushes
therefore never move the mutable `:latest` pointer -- the OCI-tag analogue of the
apex-`latest` no-hijack rule. An explicitly passed `tags` input skips the compute
step entirely (byte-identical to the previous behavior).
