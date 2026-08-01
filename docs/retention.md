# Retention and garbage collection

`internal/retention/`. Extracted verbatim from CLAUDE.md; paragraph breaks were
added at the existing topic boundaries, no wording changed. See also
`docs/eviction-policies.md` (why there is no repackage-cache eviction) and
`docs/artifact-storage-records.md` (how eviction retracts what it published).

Eviction policy + reference-counted garbage collection. Keeps the latest
`BUILDHOST_RETENTION_KEEP_N` published releases per `(project, git_branch)` and
sweeps abandoned (unpublished) uploads, then deletes content-addressed blobs no
longer referenced by anything (the global `db.IsBlobReferenced`, generalizing
`BlobBelongsToProject`).

The whole eviction runs in one `db.EvictReleases` transaction that is **rolled
back for dry-run / committed for enforce**, so report-only and enforce produce
identical exact results (and a blob shared by several evicted releases is freed
once). Single source of truth for the background sweeper
(`cmd/buildhost/serve.go`), the `buildhost gc` CLI, and the **admin dashboard
Retention page**.

The policy (keep-N, recency guard) is **DB-backed and UI-editable** -- stored in
the single-row `retention_settings` table, seeded from the
`BUILDHOST_RETENTION_*` env defaults on first start (`SeedRetentionSettings`,
INSERT OR IGNORE) and managed thereafter from the dashboard; the sweeper and CLI
read it live each run via `db.GetRetentionSettings` +
`retention.ConfigFromSettings`. The admin endpoints
(`internal/admin/retention.go`: `GET/PUT /api/retention`, `POST
/api/retention/run`) expose the policy, a dry-run preview (`Plan`), and on-demand
enforce -- an ENFORCING on-demand run 409s while writes are in flight (the same
`admin.InflightWrites` guard the background sweeper uses, so it cannot free a blob
a mid-flight hash-reference upload just validated); report-only runs are always
allowed. `keep_n=0` still keeps each branch tip (the keep-N query floors at
`max(keep_n, 1)`).

**Report-only by default**: the background sweeper deletes only when
`BUILDHOST_RETENTION_ENFORCE=true`; a manual dashboard/CLI run deletes when the
operator confirms `--enforce`/the run button. Pins (never evicted): each branch's
latest published release, oci-tagged releases, `kind=docker` releases, and
anything newer than the recency guard. The shared `DeleteBlobIfUnreferenced`
helper also fixes the sites delete/re-upload paths, which previously
`Store.Delete`d unconditionally and could break a dedup-shared blob. Background
sweeper is opt-in via `BUILDHOST_RETENTION_INTERVAL` (0 = off) and defers while
writes are in flight (`admin.InflightWrites()`).

NOTE: there is no standalone repackage-cache eviction -- non-OCI formats are
regenerated per request, not stored (see `docs/eviction-policies.md`); dedicated
docker/OCI blob GC is deferred.
