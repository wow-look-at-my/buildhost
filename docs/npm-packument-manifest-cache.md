# npm packument manifest cache

## What the packument reflects (extracted verbatim from CLAUDE.md)

A pre-built `kind=npm-package` artifact's packument **reflects the uploaded tarball's own `package.json` manifest**. It does not emit a bare `{name,version,dist}` stub. `npmManifestFields` reads `package/package.json` from the stored tarball. It surfaces `dependencies`, `optionalDependencies`, `peerDependencies`, `peerDependenciesMeta`, `bundle(d)Dependencies`, `bin`, `os`, `cpu` and `engines`. Without that, a package whose runtime depends on those fields publishes with an empty dependency graph and installs as an inert no-op. A launcher whose `optionalDependencies` are its per-platform binary packages is one such package. npm resolves against the packument, not the tarball. `name`, `version` and `dist` stay buildhost-authoritative. A lifecycle `scripts` block is deliberately never surfaced, because serving a packument must not imply that install hooks run. An unreadable blob falls back to the minimal entry.

## The defect

`https://npm.pazer.build/@buildhost/cc-marketplace__jq` appeared to hang: no HTTP status, zero bytes, every client timing out. It was not a hang. The packument took **44.4 seconds**, and `json.NewEncoder(w).Encode(info)` runs at the very end, so nothing reached the client until it finished.

The cost was structural. A packument describes EVERY published release, and for a pre-built `kind=npm-package` release each version entry reflects the uploaded tarball's own `package.json` (dependencies, optionalDependencies, bin, os, cpu, engines -- `manifestPassthroughFields`). That reflection is not optional. A launcher package whose packument omits its `optionalDependencies` installs as an inert no-op. npm resolves against the packument, not the tarball.

Reading it meant decompressing the stored blob, per release, per request:

- `cc-marketplace/jq`: **238 published releases**, tarball **17.6 MB** each.
- `package/package.json` is the **12th** tar entry, at decompressed offset **~33.7 MB** -- behind four platform binaries.
- A `.tgz` is one DEFLATE stream with no index, so reaching a member at offset N means inflating all N bytes. There is no seeking to it.

So one packument request decompressed ~8 GB (zstd out of storage, then gzip), serially, and got *slower with every publish*. The project publishes on every push.

## The fix

Cache the extracted fields per artifact in `packaged_artifacts` under format `npm-manifest` (`internal/npm/manifest.go`). It is the same digest-cache pattern brew uses for a tar.gz sha256 and apt uses for a deb digest.

- **No blob is stored.** `storage_key` and `size` mirror the SOURCE artifact exactly, so retention's freed-bytes UNION dedupes the row against the artifact's own. The answer itself lives in the row's `metadata` JSON. The row rides the existing per-release retention cascade. There is no migration and no schema change.
- **Extraction is lazy and on demand.** It runs on the first packument that needs a given artifact, never at publish time, streaming straight from the blob store. No temp file is written anywhere.
- **`fields_version`** pins the extraction contract. Bump `npmManifestFieldsVersion` when `manifestPassthroughFields` changes. Every existing row then reads as a miss and refills in place. A packument can therefore never advertise a field set the server no longer produces.
- **A verdict of "nothing to surface" is cached too.** A blob that is not a readable npm tarball caches an empty map. That covers a blob that is not gzip, one with no `package.json`, and one with bad JSON. The answer cannot change for a content-addressed blob. To re-derive it per request preserves the whole defect for exactly those packages. Only a failure to READ the blob is left uncached, such as a storage error or a cancelled context.

Cold requests are bounded three ways:

- **Blob grouping.** Storage is content-addressed. Releases that re-register unchanged bytes, through a hash-reference upload, therefore share a blob. It is decompressed once, and every sharing artifact gets its own cache row.
- **Bounded concurrency** (`manifestFillConcurrency = 8`). Each fill streams one artifact through zstd+gzip, so memory stays bounded by the decoder windows. The work is CPU-bound: on a 4-core box 8 workers give ~3.2x, and raising the bound past the core count buys nothing but memory pressure.
- **A hard budget** (`manifestFillBudget = 20s`). Overrunning it returns `503` + `Retry-After: 5`, never a `200` whose version entries quietly lost their dependency graph. Fills already committed survive, so each retry has less to do and a cold project converges instead of failing forever.

## Measured

Against a real `buildhost serve` seeded with 238 published releases of the actual 17.6 MB `cc-marketplace__jq` tarball:

| | before | after |
|---|---|---|
| cold packument | 44.4 s (measured live) | 13.3 s |
| warm packument | 44.4 s, every time | **0.016 s** |
| shared-blob cold (238 releases, 1 blob) | 44.4 s | 1.16 s |

## Producer-side follow-up

buildhost cannot seek inside a gzip member. A publisher can put `package/package.json` FIRST in the tarball, which is what `npm pack` itself does. A cold extraction then reads about 1 KB instead of 33 MB. `cc-marketplace`'s `marketplace-build package-plugin` builds its tarball with `tar -cf - -C dir .`, which is readdir order. That is how `package.json` ended up behind the platform binaries. A fix there makes a cold packument nearly free. The cache is what keeps buildhost correct for every other publisher.

## Regression checks

`internal/npm/packument_cache_test.go` asserts by COUNTING BLOB READS rather than by timing, so it fails deterministically if per-request work ever becomes proportional to the release count again:

- a warm packument reads **zero** blobs and serves byte-identical JSON to the cold one.
- a cold packument reads each release's blob exactly once, and **once in total** when the releases share a blob.
- an unreadable blob is read once, then never again.
- an exhausted budget returns `503` with `Retry-After`, not a stripped `200`. The next request converges to a full packument.
