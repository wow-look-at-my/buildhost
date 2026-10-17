# Large uploads

buildhost accepts a single upload up to 2 GiB. A proxy in front of it may not. Cloudflare's edge rejects a request body over 100 MB, with a 413 that never reaches the origin. Several ways around that follow, and each is reliable on the first try.

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

## Chunked upload session API

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
