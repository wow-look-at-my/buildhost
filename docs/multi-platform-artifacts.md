# One file, several platforms

An Actually Portable Executable is ONE file that boots natively on Linux, macOS
and Windows. Publishing it as N per-platform artifact rows -- the upload-time
fan-out described in `docs/uploads.md` -- gives a consumer N download links for
one binary, and gives a UI N rows for one file. Single-artifact multi-platform
ingest is the other answer: one blob, one artifact row, one download link, N
occupied platform slots.

Both remain. Fan-out is right for N genuinely separate builds that happen to
share bytes. This path is right for one file that genuinely runs everywhere.

## The upload endpoint

```
PUT /api/v1/projects/{project}/releases/{version}/artifacts/ape?platforms=<os/arch,...>
```

The literal `ape` path segment replaces the `{os}/{arch}` pair; the platform set
lives in `platforms`.

| Parameter | Required | Meaning |
|---|---|---|
| `platforms` | yes | Comma-separated `os/arch` pairs. Each side accepts the alias spellings `db.NormalizeOS`/`db.NormalizeArch` accept (`macOS/aarch64` is `darwin/arm64`). |
| `kind` (or `X-Artifact-Kind`) | no | Defaults to `binary`. `docker` and `npm-package` are rejected. |
| `upload_session` | no | Chunked-session finalize, identical to the slot endpoint. |
| `upload_sha256` | no | With an empty body, a hash-reference upload; the referenced blob's own bytes are what the format check reads. |
| `X-Artifact-Filename` | no | The name the download is served under. |

Auth, size caps and the release-already-published rule are the slot endpoint's,
unchanged.

```bash
curl -fSL -X PUT \
  -H "Authorization: Bearer $TOKEN" \
  -H "X-Artifact-Filename: go-toolchain" \
  --data-binary @build/go-toolchain_cosmo_fat \
  "https://pazer.build/api/v1/projects/go-toolchain/releases/v42/artifacts/ape?platforms=linux/amd64,darwin/arm64,windows/amd64"
```

From GitHub Actions, with the same OIDC identity every other publish uses:

```yaml
- uses: wow-look-at-my/buildhost/.github/actions/buildhost-publish@master
  with:
    project: go-toolchain
    path: build/
```

and a `buildhost-artifacts.json` in that directory naming the portable files:

```json
{
  "schema": 1,
  "artifacts": [
    {
      "file": "go-toolchain_cosmo_fat",
      "filename": "go-toolchain",
      "platforms": ["linux/amd64", "darwin/arm64", "windows/amd64"]
    }
  ]
}
```

A file listed there is published through this endpoint and removed from the
action's `<binary>_<os>_<arch>` filename scan. Anything not listed keeps today's
per-platform behavior.

### Validation

Every rejection is a 4xx that names the offending token, and stores nothing:

- an unknown os or arch, a pair without a `/`, an empty element, an empty set,
  or a duplicate after normalization (`darwin/arm64,macos/aarch64`);
- an incoherent pair (`linux/wasip1` -- `os=wasm` pairs only with `js`/`wasip1`);
- a platform already taken in this release for this kind (409, naming it), from
  either direction: a per-platform upload for a covered platform conflicts too;
- **a multi-platform set whose file is not an Actually Portable Executable.**

That last one is the point of the badge. A claim that one file runs on three
platforms is only true for a format that carries three platforms' code, so
`internal/exeformat` reads the leading bytes and requires the `MZqFpD` magic
Cosmopolitan's stub opens with. A single-platform `platforms=` value is not a
portability claim, so any file may take this path.

## Storage model

`artifact_platforms` (migration 017) is the authority on which slots an artifact
occupies. Every artifact has at least one row there, including the
single-platform ones the migration backfills, so lookup and slot-uniqueness have
one code path rather than two:

```sql
CREATE TABLE artifact_platforms (
    artifact_id INTEGER NOT NULL REFERENCES artifacts(id),
    release_id  INTEGER NOT NULL REFERENCES releases(id),
    kind        TEXT NOT NULL,
    os          TEXT NOT NULL,
    arch        TEXT NOT NULL,
    ordinal     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (artifact_id, os, arch)
);
CREATE UNIQUE INDEX idx_artifact_platforms_slot
    ON artifact_platforms(release_id, kind, os, arch);
```

The unique index enforces exactly what `artifacts.UNIQUE(release_id, os, arch,
kind)` enforces for canonical slots, which is why a conflict is detected in both
directions. `ordinal` is the publisher's declared order; ordinal 0 is the
**canonical slot**, mirrored into `artifacts.os`/`arch`.

`artifacts.exe_format` records what the leading bytes said the file is (`ape`,
or `''` when nothing was recognized). The badge reads off this rather than
assuming, so adding a second portable format later is a detector plus a label,
not a schema change.

## Resolution

`GetArtifactByReleaseOSArch` joins through `artifact_platforms`, so every
download path -- `/dl`, `/static`, apt, signed download links -- resolves a
covered platform to the one artifact with no per-caller change.

`/dl` additionally folds the requested pair to the artifact's canonical slot
before building the `static.{domain}/file` URL. Without that fold, three
platforms of one file would produce three static URLs, a CDN would cache three
copies of one blob, and a UI comparing two platforms' links would show two URLs
for one binary. With it, `dl/{project}?os=darwin&arch=arm64` and
`dl/{project}?os=linux&arch=amd64` return the same Location, and that one object
has one digest and one ETag. A pair the artifact does not cover is left
untouched and `static` answers the 404 as before.

`latest` and branch resolution are unchanged: the fold happens after the release
is resolved, so the apex-`latest` default-branch rule (`docs/apex-latest.md`)
still decides which release is served.

## What it means for apt, brew, npm and oci

**A multi-platform artifact reaches exactly the platforms it would have reached
as N separate rows.** Multi-platform ingest changes the row count and the number
of download links; it changes nothing about coverage. So a Homebrew formula
still gets a `darwin/arm64` bottle, apt still gets a `linux/arm64` deb, and the
OCI index still lists every covered platform -- all pointing at the same stored
blob.

`db.ListArtifactsByPlatform` is what those surfaces consume: one entry per
covered platform, with `os`/`arch` rewritten to that platform. Each entry
carries a `CacheSuffix`, because `packaged_artifacts` is keyed on `(artifact_id,
format)` and two platforms of one file would otherwise share one derived
package -- a `linux/arm64` deb served as the `linux/amd64` deb, or one
platform's OCI config unlinked when the next is generated. The canonical slot's
suffix is `""`, so every pre-existing cache row keeps its exact key; a
non-canonical platform's is `@os/arch`.

## What a consumer sees

The artifact JSON carries `platforms` on every artifact, single-platform ones
included, so a consumer reads one field and never special-cases:

```json
{
  "id": 41,
  "os": "linux",
  "arch": "amd64",
  "kind": "binary",
  "sha256": "…",
  "exe_format": "ape",
  "platforms": [
    {"os": "linux", "arch": "amd64"},
    {"os": "darwin", "arch": "arm64"},
    {"os": "windows", "arch": "amd64"}
  ]
}
```

The public release page renders one row per FILE with one set of download links
and a badge reading `APE: linux/amd64, darwin/arm64, windows/amd64`. The admin
dashboard's artifact tables render the same set in their platform badge.

## Tests

- `internal/exeformat` -- the magic check, including a bare `MZ` PE header and a
  magic that is not at offset 0.
- `internal/db/platforms_test.go` -- set parsing, one-row-many-slots, conflicts
  leaving nothing behind, the per-platform flattening and its cache keys.
- `internal/api/artifacts_ape_test.go` -- the endpoint: validation, the non-APE
  rejection, conflicts in both directions, hash-reference uploads.
- `internal/dl/multiplatform_test.go` -- every covered platform folds to one
  static URL; an uncovered one does not.
- `internal/server/multiplatform_test.go` -- end to end over real HTTP: one
  upload, five request spellings, one Location, one digest, one ETag, and the
  rendered badge on the release page.
