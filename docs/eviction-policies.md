# Eviction / Retention Policy Design

Status: **Implemented** -- keep-N per `(project, git branch)` + abandoned-upload
sweep + reference-counted blob GC, report-only by default. Background sweeper,
`buildhost gc` CLI, and an admin "Reclaimable" estimate. Size-watermark / LRU
(needs a `last_accessed_at` signal) and dedicated docker/OCI GC remain deferred.
Scope: how buildhost reclaims storage by evicting old releases safely, given
content-addressed deduplication.

> **Correction from the original draft (Phase 1 "cache eviction" was dropped).**
> The draft assumed the on-demand repackage formats (tar.gz/xz/zst, zip, deb,
> brew, npm) were cached in `packaged_artifacts` and grew per download. They are
> not: `internal/static/fmt_repackage.go` re-derives them from the original
> artifact on every request and never stores them. In production
> `packaged_artifacts` holds only the synthesized-OCI blobs (`oci-base-layer`,
> `oci-layer`, `oci-config`), which do not grow per download and are entangled
> with by-digest OCI pulls. So there is no standalone "regenerable cache" to TTL.
> Those OCI blobs are instead reclaimed naturally when their owning release is
> evicted by keep-N (the cascade drops their rows and the refcount sweep frees
> any now-unreferenced blob). The real reclaim is keep-N + the abandoned sweep.

---

## 1. Problem statement

buildhost currently **never deletes a release or artifact**. The only
`DELETE FROM` statements in the codebase target `sites`, `api_tokens`, and
`oidc_policies` (`internal/db/queries/{sites,tokens,oidc}.sql`). There is no
`DeleteRelease`, no `DeleteArtifact`, and no garbage collection. Every publish
adds rows and blobs that live forever.

At the time of writing the instance holds ~35 projects / ~731 releases / ~3601
artifacts and the dashboard reports ~77.7 GiB "Storage Used". With CI pushing a
new release per branch per commit, this grows without bound. We need a way to
bound it.

This document proposes a design. It deliberately does **not** ship code; it
exists to align on the model and the safety rules first.

---

## 2. Current architecture (what eviction has to work within)

### 2.1 Storage is content-addressed and deduplicated

`internal/storage/filesystem.go`:

- `Put` streams to a temp file, computes the SHA-256, and uses the hex digest as
  the key. If a blob with that key already exists on disk it returns early
  (`fs.root.Stat(rel)` hit) -- **identical content is stored once**.
- Blobs are zstd-compressed on disk (unless `BUILDHOST_STORAGE_COMPRESS=false`),
  with a `BHC\x01` magic + original-size header.
- `Delete` is a bare `os.Remove(rel)` with **no reference counting whatsoever**.

### 2.2 One blob can be referenced from five columns

Because the key is the content hash, a single physical blob may be pointed at by
any of:

| Table | Column(s) |
|---|---|
| `artifacts` | `storage_key`, `stripped_storage_key`, `debug_storage_key` |
| `packaged_artifacts` | `storage_key` (on-demand repackage cache) |
| `sites` | `storage_key` |
| `oci_blob_links` | `storage_key` (pushed docker blobs + synthesized OCI layers) |

The existing per-project gate `BlobBelongsToProject`
(`internal/db/queries/artifacts.sql`) already encodes most of this union -- but
**scoped to one project**. Eviction needs the *global* version: "is this blob
referenced by anything, anywhere?"

### 2.3 The sharpest dedup example: the OCI base layer

`internal/repackage/oci.go` registers a shared "essentials" base layer
(`oci-base-layer`) that is content-addressed and **deduped to a single blob
server-wide**, then linked per pull. That one blob backs every synthesized OCI
image across every project. Any eviction that deletes blobs by release ownership
would eventually delete this blob out from under hundreds of live images.

> **Takeaway:** blobs must never be deleted because "their release went away."
> They may only be deleted when *no reference of any kind remains*.

### 2.4 A latent bug we inherit

`internal/sites/delete.go` already deletes inline:

```go
storageKey, err := h.DB.DeleteSite(ctx, project.ID, rt.branch)
...
if storageKey != "" {
    _ = h.Store.Delete(ctx, storageKey) // unconditional
}
```

If two site deploys (e.g. two branches at the same commit) produce byte-identical
tarballs, they share one blob. Deleting one branch removes the shared blob and
breaks the other. Low probability today, but it is the exact class of bug the
eviction design must not repeat -- and the reference-counted approach below fixes
it for free.

### 2.5 Stats machinery already exists

`internal/db/queries/admin.sql` `GetDashboardStats` already computes:

- `total_storage_bytes` = `SUM(artifacts.size)` -- this is the **77.7 GiB**
  headline. Logical, uncompressed, **originals only** (excludes stripped/debug
  and packaged cache).
- `logical_bytes` = artifacts + stripped + debug + packaged (full logical
  footprint).
- `physical_bytes` = dedup-aware `SUM(MAX(size) per distinct storage_key)` --
  still uncompressed, so actual on-disk is smaller again after zstd.

`GetStorageBreakdown` gives per-project totals. The admin endpoint
`GET /api/storage` (`internal/admin/admin.go:583`) already returns
`logical_bytes` / `physical_bytes`. A "reclaimable bytes" figure slots in here.

---

## 3. The foundation: reference-counted mark-and-sweep GC

Everything else depends on this. **Policy decides *what* to forget; GC decides
*when a blob is safe to delete*.** Keep them separate.

### 3.1 Two-phase deletion

1. **Mark (policy, transactional):** delete DB rows for the evicted unit
   (release -> its artifacts -> their packaged_artifacts + download_counts ->
   oci_tags), collecting the set of `storage_key`s those rows referenced.
2. **Sweep (GC):** for each collected candidate key, delete the blob **iff** it
   is no longer referenced by any of the five columns in 2.2.

A blob with zero references is always safe to delete, and re-running the sweep is
idempotent -- so the process is crash-safe. If the server dies between phases,
the next sweep finishes the job.

### 3.2 The "is this blob still referenced?" query

The global generalization of `BlobBelongsToProject`:

```sql
-- name: IsBlobReferenced :one
SELECT EXISTS(
    SELECT 1 FROM artifacts
      WHERE storage_key = @key OR stripped_storage_key = @key OR debug_storage_key = @key
    UNION ALL SELECT 1 FROM packaged_artifacts WHERE storage_key = @key
    UNION ALL SELECT 1 FROM sites             WHERE storage_key = @key
    UNION ALL SELECT 1 FROM oci_blob_links    WHERE storage_key = @key
) AS referenced;
```

### 3.3 Targeted sweep vs. full reconciliation

- **Targeted sweep** (primary): only check the candidate keys produced by a
  delete. Cheap, runs right after eviction. No races because we only consider
  keys whose rows we just removed.
- **Full reconciliation** (periodic safety net): walk the storage root and delete
  any on-disk key that `IsBlobReferenced` says is dead.
  - **Must use a grace period.** The publish path does `Store.Put` *then* inserts
    the artifact row; there is a window where a blob is on disk with no DB row
    yet. A full sweep must skip blobs whose mtime is newer than, say, 24h to
    avoid deleting an in-flight upload. The targeted sweep avoids this window
    entirely.

### 3.4 Fixing the sites bug

`sites/delete.go` should stop calling `Store.Delete` inline and instead route the
freed key through the same targeted sweep (delete the blob only if no other
reference remains). This is a small, self-contained correctness fix that lands
with the GC foundation.

---

## 4. Safety rails: what must never be evicted (pins)

Any policy must treat these as hard pins, independent of age or count:

1. **A project's latest published release.** Always keep at least one.
2. **The latest published release per `git_branch`.** Branch downloads
   (`/dl/{project}/branch/{branch}/...`, resolved by
   `GetLatestPublishedReleaseByBranch`) break otherwise.
3. **Any release referenced by `oci_tags.release_id`.** Tags are mutable pointers;
   deleting the backing release breaks `docker pull repo:tag`.
4. **In-flight / very recent releases.** Don't evict something created in the last
   N hours (avoid racing an active publish/repackage).
5. **(Implicit via GC) shared blobs** such as the OCI base layer -- never deleted
   while any reference remains.

Unpublished releases (`published = 0`) are the opposite: they are abandoned
partial uploads and are the *safest* thing to clean up (see Phase 2).

---

## 5. Signals available for policy (and the one we lack)

| Signal | Source | Use |
|---|---|---|
| Age | `releases.created_at` / `published_at`, `artifacts.created_at` | TTL |
| Recency of version | `releases.version_num` | keep-last-N |
| Branch | `releases.git_branch` | per-branch keep / pin |
| Tag pin | `oci_tags.release_id` | never-evict |
| Publish state | `releases.published` | sweep abandoned uploads |
| Popularity | `download_counts.count` | tie-breaker / "keep if popular" |
| **Last access time** | **(none)** | **needed for true LRU** |

We track download **count** but never **when** a download happened. A genuine
"evict the coldest artifacts" / LRU policy would require a new
`artifacts.last_accessed_at` (or a separate access-log table) updated on the
download path. This is the main schema gap that distinguishes the policy options
below.

---

## 6. Policy model options

All three sit on top of the GC foundation and honor the Section 4 pins. They are
not mutually exclusive -- keep-N and a size watermark compose well.

### Option A -- Keep last N published releases per project (recommended default)

Keep the N most-recent published releases per project; evict older ones. Plus
pins (latest-per-branch, tagged, recent).

- **Pros:** predictable, easy to explain, registry-standard (npm dist-tags,
  ghcr retention), uses only signals we already have (`version_num`). Bounds
  per-project growth directly.
- **Cons:** doesn't directly target a disk budget; a few huge projects can still
  dominate. N is a blunt instrument across very different projects.
- **Config:** `BUILDHOST_RETENTION_KEEP_N` (global default), optional per-project
  override column.

### Option B -- Age-based TTL

Evict published releases older than X days (plus pins).

- **Pros:** simple, time-bounded, good for "previews older than a month go away".
- **Cons:** bursty projects can still blow the budget within the window; quiet
  projects lose history they may still want. Doesn't bound total size.
- **Config:** `BUILDHOST_RETENTION_MAX_AGE` (e.g. `30d`).

### Option C -- Global size watermark / LRU

Evict (coldest first) until total physical size is under a target.

- **Pros:** directly targets the actual constraint (disk). Adaptive.
- **Cons:** needs the missing `last_accessed_at` signal to be meaningful;
  otherwise "coldest" degrades to "oldest" (= a global TTL). More moving parts;
  eviction amount varies run to run.
- **Config:** `BUILDHOST_RETENTION_TARGET_BYTES` (high/low watermark).

### Recommendation

Ship **Option A (keep-N) + the always-safe Phase 1/2 cleanups**, defaulting to
report-only. Add **Option C** later if a hard disk budget becomes necessary, at
which point we invest in `last_accessed_at`. Option A covers the actual growth
driver (per-branch-per-commit CI releases) with signals we already have and zero
new tracking.

---

## 7. Phased rollout

Ordered safest-first; each phase is independently shippable and valuable.

**Phase 0 -- GC foundation (Section 3).** `DeleteRelease` cascade + global
`IsBlobReferenced` + targeted sweep. Also fixes the `sites/delete.go` dedup bug.
No automatic eviction yet; this is the safe primitive everything else calls.

**Phase 1 -- Evict the regenerable cache.** `packaged_artifacts` are rebuilt on
the next download, so dropping ones older than a TTL is **zero-risk** and likely
the biggest immediate win (every format ever requested is cached forever right
now). Exception: keep `oci-base-layer` entries (shared, cheap, hot). Config:
`BUILDHOST_RETENTION_CACHE_TTL` (e.g. `30d`).

**Phase 2 -- Sweep abandoned unpublished releases.** Delete `published = 0`
releases older than a few hours and their artifacts (failed/partial uploads).
Safe by construction -- nothing serves an unpublished release.

**Phase 3 -- Keep-last-N retention (Option A).** Per-project, honoring all pins,
**report-only by default**. The operator reviews the report, then flips
enforcement on.

**Phase 4 (optional, later) -- Size watermark (Option C)** + `last_accessed_at`
tracking if a hard budget is required.

Docker/OCI eviction is called out separately below and is **not** in Phases 0-3
beyond pins.

---

## 8. Schema / query changes (sketch, not final)

Next migration number is `009` (`migrations/` is sequential `NNN_name.sql`,
each applied in its own transaction; mirror into `internal/db/schema.sql` for
sqlc).

- **No new tables required for Phases 0-3.** New queries only:
  - `DeleteRelease` cascade set: delete `download_counts` and `packaged_artifacts`
    for the release's artifacts, delete `artifacts`, delete `oci_tags` for the
    release, delete the `releases` row -- all in one transaction, returning the
    candidate `storage_key`s.
  - `IsBlobReferenced` (Section 3.2).
  - `ListEvictableReleases` per project: published releases ranked by
    `version_num DESC`, excluding the latest-per-branch and tagged releases, with
    `OFFSET N` for keep-N.
  - `ListExpiredPackagedArtifacts` (Phase 1), `ListAbandonedReleases` (Phase 2).
  - `SumReclaimableBytes` for the admin surface.
- **Optional later:**
  - `artifacts.last_accessed_at DATETIME` (Phase 4 / Option C).
  - `projects.retention_keep_n INTEGER` (per-project override of the global N).

---

## 9. Where it runs

Three surfaces, matching existing patterns:

- **Background sweeper:** a goroutine on a ticker in the server, like graceful
  shutdown. Must respect context cancellation on SIGTERM and should not fight the
  inflight-write tracking used by `/ready-to-update`. Interval via
  `BUILDHOST_RETENTION_INTERVAL` (e.g. `1h`); off by default.
- **CLI subcommand:** `buildhost gc` / `buildhost evict` (cobra, one file,
  self-registering via `init()`, per the repo's CLI conventions) with
  `--dry-run` for cron/manual use. `--dry-run` reuses the report path.
- **Admin surface:** extend `GET /api/storage` with reclaimable bytes per project;
  optionally a `POST /api/gc` trigger (there is precedent for admin mutations,
  e.g. `DELETE /api/tokens/{id}`). Admin remains behind the reverse proxy.

**Default posture: report-only.** Every destructive phase computes and logs/returns
"what would be deleted and how many bytes" before any enforcement flag is set.
Enforcement is opt-in via env.

---

## 10. Docker / OCI eviction (separate, harder -- later)

`oci_blob_links` is **project-scoped, not release-scoped**: pushed docker blobs
and manifests attach to the project, shared across pushes and tags
(`internal/oci/*.go`). So evicting a `kind=docker` release is not a simple row
cascade -- you must drop the tag/manifest, then GC blobs no longer reachable from
any remaining tag/manifest in that project. This deserves its own design pass and
is out of scope for the initial keep-N work. Until then, docker images are
retained (and tagged releases are pinned regardless).

---

## 11. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Delete a shared blob still in use | Reference-counted sweep (Section 3); never delete by ownership |
| Race with in-flight publish (blob on disk, row not yet written) | Targeted sweep only touches just-deleted keys; full sweep uses a mtime grace period |
| Evict something a download URL still resolves | Pins: latest overall, latest-per-branch, tagged (Section 4) |
| Operator surprise / data loss | Report-only default; dry-run CLI; WARN-level logs naming every evicted release |
| Crash mid-eviction | Two-phase + idempotent sweep; DB cascade in one transaction |
| Docker blob sharing mishandled | Docker eviction deferred to its own phase (Section 10) |

---

## 12. Open decisions (for the team)

1. **Policy model:** confirm keep-N (Option A) as the default, or prefer TTL /
   size-watermark?
2. **Default N** (e.g. 10?) and whether per-project overrides are needed up front.
3. **Cache TTL** for Phase 1 (e.g. 30d) -- aggressive is fine since it regenerates.
4. **Enforcement rollout:** ship report-only first and flip per-instance, or gate
   behind an explicit `BUILDHOST_RETENTION_ENFORCE=true`?
5. **Do we invest in `last_accessed_at`** now (enables true LRU/Option C) or defer
   until a hard disk budget forces it?
6. **Branch handling:** keep-N per project, or keep-N *per branch* (CI branches
   are the real multiplier)?
