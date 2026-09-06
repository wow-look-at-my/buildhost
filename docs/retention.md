# Retention and garbage collection

`internal/retention/`. Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed. See also `docs/eviction-policies.md`, on why there is no repackage-cache eviction, and `docs/artifact-storage-records.md`, on how eviction retracts what it published.

Eviction policy + reference-counted garbage collection. Keeps the latest `BUILDHOST_RETENTION_KEEP_N` published releases per `(project, git_branch)` and sweeps abandoned (unpublished) uploads, then deletes content-addressed blobs no longer referenced by anything (the global `db.IsBlobReferenced`, generalizing `BlobBelongsToProject`).

The whole eviction runs in one `db.EvictReleases` transaction. It is **rolled back for a dry-run and committed for an enforce**. Report-only and enforce therefore produce identical exact results. A blob shared by several evicted releases is freed once. This is the single source of truth for the background sweeper (`cmd/buildhost/serve.go`), the `buildhost gc` CLI, and the **admin dashboard Retention page**.

The policy, keep-N and the recency guard, is **DB-backed and UI-editable**. It is stored in the single-row `retention_settings` table. `SeedRetentionSettings` seeds it from the `BUILDHOST_RETENTION_*` env defaults on first start, with an INSERT OR IGNORE. The dashboard manages it after that. The sweeper and the CLI read it live on each run through `db.GetRetentionSettings` and `retention.ConfigFromSettings`.

The admin endpoints are in `internal/admin/retention.go`: `GET/PUT /api/retention` and `POST /api/retention/run`. They expose the policy, a dry-run preview (`Plan`) and an on-demand enforce. An ENFORCING on-demand run 409s while writes are in flight. It uses the same `admin.InflightWrites` guard the background sweeper uses, so it cannot free a blob a mid-flight hash-reference upload just validated. A report-only run is always allowed. `keep_n=0` still keeps each branch tip, because the keep-N query floors at `max(keep_n, 1)`.

**Report-only by default.** The background sweeper deletes only when `BUILDHOST_RETENTION_ENFORCE=true`. A manual dashboard or CLI run deletes when the operator confirms it with `--enforce` or the run button. Four things are pinned and never evicted: each branch's latest published release, an oci-tagged release, a `kind=docker` release, and anything newer than the recency guard. The shared `DeleteBlobIfUnreferenced` helper also fixes the sites delete and re-upload paths. Those called `Store.Delete` unconditionally, which breaks a dedup-shared blob. The background sweeper is opt-in through `BUILDHOST_RETENTION_INTERVAL`, where 0 is off. It defers while writes are in flight (`admin.InflightWrites()`).

NOTE: there is no standalone repackage-cache eviction. A non-OCI format is regenerated per request and never stored. See `docs/eviction-policies.md`. A dedicated docker and OCI blob GC is deferred.

## The file inventory (why so little is reclaimable)

A reclaimable total says how much comes back. It never says what holds the rest. `GET /api/retention/inventory` on the admin port answers that. It returns one entry per stored file, plus the pins that keep it. The Retention page has a **Copy file inventory (JSON)** button and a **Download file inventory (JSON)** button. Both fetch this endpoint and hand you the whole document.

`retention.Inventory` builds it. It reads every table that references a blob. Those are `artifacts` (raw, stripped and debug keys), `packaged_artifacts`, `sites`, `oci_blob_links` and `goproxy_versions`. It emits one entry per reference. Each entry carries the storage key, sha256, size, timestamp, project, version, branch, platform, kind and filename. `refs` counts the rows that share that storage key. A blob with two references therefore frees nothing until both releases go.

Each entry carries `holds`, every pin that applies, most permanent first, and `hold`, the first of them:

| hold | what pins the file |
| --- | --- |
| `branch-tip` | the newest published release on its branch |
| `keep-n` | inside the keep-N window on its branch |
| `recency-guard` | created after the recency cutoff |
| `oci-tag` | an OCI tag points at the release |
| `docker` | a pushed-docker release |
| `draft` | a deliberate draft, never swept |
| `shared-blob` | the release goes, another row still references the bytes |
| `site`, `oci-blob`, `goproxy` | not release-scoped, so eviction never frees it |

An empty `hold` with `reclaimable: true` means the current policy frees the blob now. `by_hold` groups the whole server by that reason, biggest first. The top row is the answer to why the reclaimable number is small. `by_role` groups by which table holds the reference.

Sizes are the sizes the database records. They are the logical bytes, not the compressed bytes on disk. `bytes` counts every entry. A shared blob therefore counts once per reference. `blob_bytes` counts each storage key once.

The plan stays the source of truth. `hold` comes from the same facts the eviction queries decide on (`ListReleaseRetentionFacts`). An entry whose derived reason disagrees with the plan is reported as `unknown` and counted in `totals.hold_mismatches`. It is not printed as fact. A non-zero count there means this explanation drifted from `ListEvictableReleases`.
