# npm packument manifest cache

## What the packument reflects (extracted verbatim from CLAUDE.md)

A pre-built `kind=npm-package` artifact's packument **reflects the uploaded
tarball's own `package.json` manifest** (`npmManifestFields` reads
`package/package.json` from the stored tarball and surfaces `dependencies`,
`optionalDependencies`, `peerDependencies`/`peerDependenciesMeta`,
`bundle(d)Dependencies`, `bin`, `os`, `cpu`, `engines`) rather than emitting a
bare `{name,version,dist}` stub. Without this a package whose runtime depends on
those fields -- e.g. a launcher whose `optionalDependencies` are its
per-platform binary packages -- would publish with an empty dependency graph and
install as an inert no-op (npm resolves against the packument, not the tarball).
`name`/`version`/`dist` stay buildhost-authoritative and lifecycle `scripts` are
deliberately never surfaced (serving a packument must not imply running install
hooks); unreadable blobs fall back to the minimal entry.

## The defect

`https://npm.pazer.build/@buildhost/cc-marketplace__jq` appeared to hang: no
HTTP status, zero bytes, every client timing out. It was not a hang. The
packument took **44.4 seconds**, and `json.NewEncoder(w).Encode(info)` runs at
the very end, so nothing reached the client until it finished.

The cost was structural. A packument describes EVERY published release, and for
a pre-built `kind=npm-package` release each version entry reflects the uploaded
tarball's own `package.json` (dependencies, optionalDependencies, bin, os, cpu,
engines -- `manifestPassthroughFields`). That reflection is not optional: a
launcher package whose packument omits its `optionalDependencies` installs as an
inert no-op, because npm resolves against the packument, not the tarball.

Reading it meant decompressing the stored blob, per release, per request:

- `cc-marketplace/jq`: **238 published releases**, tarball **17.6 MB** each.
- `package/package.json` is the **12th** tar entry, at decompressed offset
  **~33.7 MB** -- behind four platform binaries.
- A `.tgz` is one DEFLATE stream with no index, so reaching a member at offset
  N means inflating all N bytes. There is no seeking to it.

So one packument request decompressed ~8 GB (zstd out of storage, then gzip),
serially, and got *slower with every publish*. The project publishes on every
push.

## The fix

Cache the extracted fields per artifact in `packaged_artifacts` under format
`npm-manifest` (`internal/npm/manifest.go`), the same digest-cache pattern brew
uses for tar.gz sha256 and apt for deb digests:

- **No blob is stored.** `storage_key`/`size` mirror the SOURCE artifact exactly
  (so retention's freed-bytes UNION dedupes the row against the artifact's own),
  and the answer itself lives in the row's `metadata` JSON. The row rides the
  existing per-release retention cascade; no migration, no schema change.
- **Extraction is lazy and on demand** -- the first packument that needs a given
  artifact, never at publish time, streaming straight from the blob store. No
  temp files are written anywhere.
- **`fields_version`** pins the extraction contract. Bump
  `npmManifestFieldsVersion` when `manifestPassthroughFields` changes and every
  existing row reads as a miss and refills in place, so a packument can never
  advertise a field set the server no longer produces.
- **A verdict of "nothing to surface" is cached too.** A blob that is not a
  readable npm tarball (not gzip, no `package.json`, bad JSON) caches an empty
  map: that answer cannot change for a content-addressed blob, and re-deriving
  it per request would preserve the whole defect for exactly those packages.
  Only a failure to READ the blob (storage error, cancelled context) is left
  uncached.

Cold requests are bounded three ways:

- **Blob grouping.** Storage is content-addressed, so releases that re-register
  unchanged bytes (hash-reference uploads) share a blob; it is decompressed once
  and every sharing artifact gets its own cache row.
- **Bounded concurrency** (`manifestFillConcurrency = 8`). Each fill streams one
  artifact through zstd+gzip, so memory stays bounded by the decoder windows.
  The work is CPU-bound: on a 4-core box 8 workers give ~3.2x, and raising the
  bound past the core count buys nothing but memory pressure.
- **A hard budget** (`manifestFillBudget = 20s`). Overrunning it returns
  `503` + `Retry-After: 5`, never a `200` whose version entries quietly lost
  their dependency graph. Fills already committed survive, so each retry has
  less to do and a cold project converges instead of failing forever.

## Measured

Against a real `buildhost serve` seeded with 238 published releases of the
actual 17.6 MB `cc-marketplace__jq` tarball:

| | before | after |
|---|---|---|
| cold packument | 44.4 s (measured live) | 13.3 s |
| warm packument | 44.4 s, every time | **0.016 s** |
| shared-blob cold (238 releases, 1 blob) | 44.4 s | 1.16 s |

## Producer-side follow-up

buildhost cannot seek inside a gzip member, but a publisher can put
`package/package.json` FIRST in the tarball -- which is what `npm pack` itself
does. Then even a cold extraction reads ~1 KB instead of 33 MB.
`cc-marketplace`'s `marketplace-build package-plugin` builds its tarball with
`tar -cf - -C dir .`, i.e. in readdir order, which is how `package.json` ended
up behind four 8 MB binaries. Fixing that there makes cold packuments nearly
free; the cache is what keeps buildhost correct for every other publisher.

## Regression checks

`internal/npm/packument_cache_test.go` asserts by COUNTING BLOB READS rather
than by timing, so it fails deterministically if per-request work ever becomes
proportional to the release count again:

- a warm packument reads **zero** blobs and serves byte-identical JSON to the
  cold one;
- a cold packument reads each release's blob exactly once, and **once total**
  when the releases share a blob;
- an unreadable blob is read once, then never again;
- an exhausted budget returns `503` + `Retry-After`, not a stripped `200`, and
  the next request converges to a full packument.
