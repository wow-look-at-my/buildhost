# Multi-platform binaries (Cosmopolitan / APE)

A single uploaded binary can be published for several operating systems and architectures, in one request. This was built for a [Cosmopolitan APE](https://justine.lol/ape.html) binary, which runs everywhere. It is usable for any platform-independent artifact.

The upload endpoint's `{os}` path segment accepts a single OS, unchanged from before. It also accepts a comma-separated list, such as `linux,darwin,windows`. It also accepts the alias `cosmo`, whose synonyms are `any`, `all` and `universal`, and which expands to `linux`, `darwin` and `windows`. The `{arch}` segment likewise accepts a list, or `any` or `all` for `amd64` and `arm64`.

```bash
# One APE binary, published for linux, darwin, and windows in one request
buildhost publish \
  --server http://localhost:8080 --token $TOKEN \
  --project myapp --os cosmo --arch amd64 \
  --artifact ./myapp.com

# Explicit list, full arch matrix (os x arch combinations)
buildhost publish ... --os linux,windows --arch any --artifact ./myapp
```

The body is streamed to content-addressed storage once. Each os/arch combination becomes an ordinary per-platform artifact row that references the same blob. The fan-out therefore costs database rows, and not bytes. Downloads, `latest` resolution, the APT, Brew, npm and OCI format handlers, and retention are all untouched. There is no stored `os=any` value, and no download-time fallback. A client still downloads a concrete `os` and `arch`.

Here are the details. Each list element is normalized like a download parameter, so `macOS` becomes `darwin` and `x86_64` becomes `amd64`. An invalid, empty or duplicate element is rejected with a 400.

Row creation is all-or-nothing. When any combination already exists, the whole request returns 409 and names it. Nothing is created then.

A single-combination upload returns the artifact JSON object exactly as before. A multi-combination upload returns a JSON array of those artifact objects, in `os` list by `arch` list order. It all works identically when you finalize a [chunked upload session](#large-uploads), with one session, one body and N rows. `kind=npm-package` keeps its literal `os=any` and `arch=any` sentinel row, and it never fans out.

## Registering more slots by hash (no re-upload)

An **exact** slot set that is not an os by arch product cannot be expressed with the fan-out grammar. `{linux/amd64, linux/arm64, windows/amd64}` is such a set, where `windows/arm64` must stay free for a different native binary. For that case, upload the file once and register the remaining slots by **hash reference**. That is an empty-body PUT that names the stored blob's SHA-256. It also skips the re-send of a byte-identical binary entirely.

```bash
SUM=$(sha256sum ./mytool | awk '{print $1}')

# First slot carries the bytes:
curl -X PUT -H "Authorization: Bearer $TOKEN" --data-binary @./mytool \
  "https://buildhost.example.com/api/v1/projects/myapp/releases/7/artifacts/linux/amd64"

# The rest reference the stored blob -- no bytes sent:
for slot in linux/arm64 windows/amd64; do
  curl -X PUT -H "Authorization: Bearer $TOKEN" \
    "https://buildhost.example.com/api/v1/projects/myapp/releases/7/artifacts/$slot?upload_sha256=$SUM"
done
```

Semantics:

- **Check the capability first.** Only send `upload_sha256` on an empty-body request when `GET /api/v1/server-info` advertises `"upload_by_sha256": true`. A server without the capability ignores the parameter and stores the empty body as the artifact.
- The referenced blob must already belong to **this project** (uploaded for any of its releases, so re-releasing an unchanged binary is nearly free). An unknown hash, another project's blob, and a since-garbage-collected blob all return the same 404 -- fall back to a full upload.
- The created rows are ordinary artifact rows, field-for-field identical to a full upload's, with the same 201 and semantics. The reference composes with the `{os}` and `{arch}` fan-out grammar. Each hash-ref request carries its own optional `X-Artifact-Filename`.
- `upload_sha256` keeps its existing meaning elsewhere. A request **with** a body ignores it. Combined with `upload_session=` it remains the session-finalize integrity check.

The in-repo publishers do this automatically when the server advertises the capability. The `buildhost-publish` GitHub action and `buildhost publish --manifest` both hash the files they are about to upload. They send each distinct file once. They register every byte-identical slot by reference. An identical APE slot copy in go-toolchain therefore transfers once, and not once per slot.
