# Uploads: chunked sessions, fan-out and hash references

`internal/uploads/`, `internal/uploadclient/`, `internal/ociclient/`, and the artifact-upload half of `internal/api/`. Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed.

## Multi-platform fan-out

The artifact upload (`PUT .../artifacts/{os}/{arch}`) supports **multi-platform fan-out**. `expandOSSpec` and `expandArchSpec` in `artifacts.go` implement it. The `{os}` segment takes a single OS, a comma list, or the alias `cosmo`. The aliases `any`, `all` and `universal` mean the same thing, and each expands to linux, darwin and windows. That alias exists for a Cosmopolitan APE binary, which runs everywhere. The `{arch}` segment takes a list, or `any` or `all`, which expand to amd64 and arm64.

`db.NormalizeOS` and `db.NormalizeArch` normalize each element, so the upload accepts the same alias spellings as a download. An invalid, empty or duplicate element answers 400.

The body streams to storage ONCE. Each os by arch combination becomes an ordinary artifact row that shares that blob. `db.CreateArtifacts` creates the rows all-or-nothing, in one transaction. A conflicting combination answers 409 and names the combination. Nothing is created then. The read path is therefore completely unchanged, which covers dl, static, the format handlers and retention.

A single canonical os/arch keeps the byte-identical single-object response. A multi-platform upload returns a JSON array of the same artifact objects. `kind=npm-package` keeps its literal `os=any` and `arch=any` sentinel row, and it never fans out.

**An APE does not fan out.** The uploaded file can carry APE magic while the segments expand to more than one combination. The upload then publishes ONE artifact with one slot per combination, rather than a row each. `publishMultiPlatform` does that, and `PUT .../artifacts/ape` shares it.

The alias spellings exist precisely to publish a file that runs everywhere. A row per platform gives such a file N download links, while it is N-way portable by construction. The response is therefore the single `ArtifactWithPlatforms` object the explicit endpoint returns, and not the array. That is the same shape a single-combination upload has always had. Its `platforms` array lists what the one row covers. Fan-out is still what a NON-APE upload gets. There the combinations really are separate builds that happen to share bytes.

**A declared platform is checked against the bytes.** `exeformat.DetectNTBoot` follows the DOS header's `e_lfanew` to the PE section count. The PE format fixes both, and no single toolchain does.

A section count of one is Cosmopolitan's do-nothing stub header. It maps none of the payload. The binary starts on Windows and exits 0 without a run, and every consumer downstream reads that as success. A declared `windows/*` platform on such a file answers 400 at ingest.

Another section count reads as a real boot header. An APE from another Cosmopolitan toolchain is therefore not rejected for a different section layout. A PE header past the sniff window (`exeformat.SniffLen`) reads as unknown, rather than as a rejection.

Multi-platform publish fan-out happens at upload time, never at download time. One uploaded blob becomes N ordinary per-platform artifact rows. A comma list, or the `cosmo` or `any` alias, in the upload URL's `{os}` and `{arch}` segments asks for it, for a non-APE upload. An APE takes the one-row path above instead.

There is deliberately no stored `os=any` value and no download-time fallback. Downloads, `latest` resolution, the format handlers, retention refcounting and CDN caching therefore all see plain per-platform artifacts. `IsBlobReferenced` counts the shared blob until the last row goes.

## Hash-reference uploads

The same PUT also accepts a **hash-reference upload**. It takes an EMPTY body (`ContentLength == 0`) with `?upload_sha256=<64-hex>` and NO `upload_session`. It registers a row, or several, for a blob the project already has in storage, and it re-sends no bytes.

That is how one uploaded binary covers an exact slot set the cartesian grammar cannot express. An example is `{linux/amd64, linux/arm64, windows/amd64}`, with `windows/arm64` left free. It is also how an unchanged re-release skips the transfer.

`resolveHashRef` in `artifacts.go` runs the flow. A bad hex shape answers 400. The same-project gate is `db.BlobBelongsToProject`. Its failure is a 404 byte-identical to the answer for an unknown blob. A public sha256 can therefore never mint a row that serves another project's bytes. `Store.Exists` follows, and answers 404 when the blob was garbage-collected. A header-only `Store.Get` then reads the row size.

The combination build, the row insert, the 409 conflict semantics and the 201 responses are shared with a full upload. Each hash-ref request carries its own `X-Artifact-Filename`, so filenames are per slot. Fan-out has one shared header instead.

Server-info advertises `upload_by_sha256: true`. A body-carrying PUT ignores the parameter. A session finalize reads it as the spool integrity check. Both are byte-identical to before.

**Fan-out comes first. Use a hash reference only for what fan-out cannot express.** A publisher that holds one file for several slots reaches for the URL grammar before it reaches for a hash. `PUT .../artifacts/linux,darwin,windows/amd64,arm64` streams the body ONCE.

The buildhost-publish action groups discovered artifacts by sha256. It sends that one request when a group's slots are exactly the product of the platforms it covers. Every cosmo build is such a group. That case is what motivated hash references at all.

Only a ragged set falls through to an upload followed by a hash reference. `{linux/amd64, linux/arm64, windows/amd64}` with no `windows/arm64` is such a set. Any status other than 201 or 409 there triggers a full upload, in case the blob was evicted in between.

`buildhost publish --manifest` still hash-refs per entry. A manifest names each slot's own `X-Artifact-Filename`, which fan-out's single shared header cannot carry.

The action does NOT probe server-info for the capability first. There is one buildhost. It advertises `upload_by_sha256`. A dedicated round-trip to re-confirm a constant is a probe for a known truth. `internal/uploadclient` still exposes `SupportsUploadBySHA256()` for the CLI. Server-info is already fetched there for the chunk threshold. The answer is therefore free.

## Chunked upload sessions (internal/uploads)

These are generic chunked upload sessions. A client can deliver an arbitrarily large request body to ANY existing upload endpoint, in pieces small enough to survive a proxy's request-body cap. Cloudflare's edge rejects a body over 100 MB with a 413 that never reaches the origin.

`POST /api/v1/uploads` creates a session. Any `write`-scoped credential may do that. The session is bound to the creator's identity, which is the token ID plus the name. That covers a DB token and an OIDC synthetic token alike.

`PATCH /api/v1/uploads/{id}?offset=N` appends a chunk, with strict offset verification. A mismatch answers 409 with the committed size, so an upload resumes. Partially transferred chunk bytes are committed. `GET` reads the size. `DELETE` aborts the session.

**Finalize by reference** is the key move. The `ResolveSessionBody` middleware is wired in `internal/server`, between `Authenticate` and routing. It intercepts any mutating request that carries `?upload_session=<id>` with an empty body. It verifies ownership, and answers 404 otherwise, so there is no existence leak. It verifies the optional `?upload_sha256=` or `X-Upload-SHA256` integrity hash. It then swaps the spool file in as `r.Body`. Every existing endpoint's routing, project auth, size caps and storage logic therefore runs unchanged.

The session is consumed on a 2xx response. It is kept for a retry on any other status. A spool lives at `{DataDir}/tmp/uploads/<id>.spool`. It is capped at `BUILDHOST_MAX_UPLOAD_SIZE` **at append time**. It is swept after `BUILDHOST_UPLOAD_SESSION_TTL`, which defaults to 24h. Three things sweep: an opportunistic sweep on create, a 15-minute janitor that serve starts, and orphan cleanup at startup.

A session is server memory plus a spool file, which is the same model as the OCI blob upload store. A container swap mid-session answers the next chunk with a clean 404, and the client restarts.

The server sits behind Cloudflare, whose edge answers 413 to a request body over 100 MB. The public `GET /api/v1/server-info` therefore advertises `max_direct_upload_bytes`. `BUILDHOST_MAX_DIRECT_UPLOAD_SIZE` sets it, and it defaults to 95 MiB. Anything larger goes through a chunked upload session.

Assemble it with `POST /api/v1/uploads` and `PATCH .../{id}?offset=N`. Then call the ORIGINAL upload endpoint with an empty body and `?upload_session=<id>`, plus an optional `?upload_sha256=`. The middleware swaps the spool in as the request body, and reuses all existing endpoint logic.

The CLI does this automatically. A client decides from the advertised limit BEFORE it sends, and never by a reaction to a 413. `BUILDHOST_MAX_UPLOAD_SIZE` still caps the total assembled size, at append time.

## The CLI upload engine (internal/uploadclient)

`publish` and `publish-site` use the CLI's upload engine. It stats the file locally. It fetches the server's advertised `max_direct_upload_bytes` from `GET /api/v1/server-info`, and falls back to a built-in 95 MiB. It then picks direct or chunked BEFORE it sends anything. The first attempt is the one that succeeds. There is no path that sends a big PUT and reacts to a 413.

A small file keeps the classic single request, byte for byte. A larger file creates a session. The engine appends sequential chunks, of 64 MiB by default. `--chunk-size` sets that, and `0` disables chunking. Each chunk has its own retry and backoff, which resumes from the server's committed size, read from a 409 or from the status endpoint. The engine finalizes with the file's sha256. On a hard failure it makes a best-effort DELETE of the session.

A missing session endpoint is a hard error, not a mode. The one buildhost advertises `upload_sessions`. The old 404 and 405 fallback had one real effect. It sent a several-hundred-megabyte single request. The proxy rejected that with a 413 nobody was able to trace back to the client.

The engine lives in `internal/`, and not in `cmd/`. Its tests therefore do not drag the untested CLI package into coverage.

## The docker-push engine (internal/ociclient)

The CLI's `docker-push` engine pushes a locally built OCI image layout to the OCI endpoint. That layout comes from `docker buildx build --output type=oci`, as a tarball or as a `tar=false` directory, or from `docker save`.

It uploads every blob larger than the server's advertised `max_direct_upload_bytes` through the OCI chunked upload session. Each `Content-Range` PATCH appends sequentially and stays under the limit. A digest-checked PUT finalizes the blob. An image layer over a fronting proxy's request-body cap therefore publishes, where the one-request-per-blob upload in docker, buildx and crane cannot. Cloudflare's edge answers 413 to a body near 100 MB.

Sizing is decided up front, from the blob size and server-info, and never by a reaction to a 413. `--chunk-size` only clamps DOWN from the advertised limit. An interrupted chunk resumes from the server's committed size, read from the 416 `Range` header or from the status GET.

A blob the registry already has is skipped after a HEAD. A blob small enough for one request still opens a session, because that POST is what asks for a cross-repo mount. It then finalizes with a single PUT that carries the bytes.

Every upload request sets `GetBody`. Without it net/http declines to retry a request once the body is written, and one mid-flight stream error fails the whole publish.

The layout walk pushes depth-first. It pushes the blobs, then each child manifest by digest, and an attestation manifest is included. It pushes the root by each tag last. It requires exactly one top-level `index.json` entry, so a push carries one image.

The `buildhost-publish-docker` action builds with buildx `--output type=oci,tar=false`. It pushes through this client, and builds the CLI from the action's own checkout. A tag that references a foreign registry still goes through buildx `push: true`.

The action computes branch-aware default tags when its `tags` input is omitted. The TypeScript step `id: default-tags` in the action does that. The defaults are the commit SHA and the docker-sanitized short ref name. Sanitization lowercases the name, folds each run of `[^a-z0-9._-]` to `-`, and strips a leading dot or dash. A `claude/foo` branch therefore yields the bare buildhost-bound tag `claude-foo`, and never a foreign `/`-reference.

`latest` is added ONLY when the push goes to the repo's default branch. The action reads that branch from the event payload. A payload-less event, such as `schedule`, falls back to `master`. That fails closed on a repo defaulted to `main`, where it adds no `latest`. A feature-branch push therefore never moves the mutable `:latest` pointer. That is the OCI-tag analogue of the apex-`latest` no-hijack rule. An explicitly passed `tags` input skips the compute step entirely, byte-identically to the previous behavior.

## One artifact covering several platforms

Fan-out answers "N builds that share bytes". The other question is "ONE file that runs on several platforms", which is an APE. `PUT .../artifacts/ape?platforms=linux/amd64,darwin/arm64,windows/amd64` answers it. That endpoint stores ONE artifact row that occupies every named slot. The result is one download link, and not N. The same `kind`, `X-Artifact-Filename`, `upload_session` and `upload_sha256` mechanics apply. A multi-platform declaration whose file carries no APE magic is rejected. Depth: `docs/multi-platform-artifacts.md`.
