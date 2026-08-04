# APT repository endpoint

`internal/apt/`. Extracted verbatim from CLAUDE.md; paragraph breaks were added
at the existing topic boundaries, no wording changed.

APT repository endpoint on `apt.{domain}/{project}/...`. Pool downloads redirect
to static. Also serves the armored public signing key at `.../{project}/key.asc`
and a generated per-project `install.sh` one-liner installer (`install.go`) that
adds the `signed-by` source and refreshes the index (the script's final `apt-get
install` hint uses the folded deb package name, see below). Self-registering via
auth.OnReady().

## Digest cache

The `Packages` index's per-artifact deb `Size`/`SHA256` are **cached in
`packaged_artifacts` under `format="deb"`** (`debdigest.go` `debDigest`: lazy fill
on first need, then a DB read instead of a full ar+gzip repackage+hash per
artifact per request -- the brew `tarGZSHA256` pattern). The row is a digest cache
only -- `storage_key` records the SOURCE artifact blob, no deb is stored, pool
downloads still repackage on demand -- which is sound because deb generation is
deterministic per input set (fixed ar member headers with zero timestamp/uid/gid,
zero tar mtimes, fixed gzip header fields, fixed member order, content-addressed
input; pinned by `TestDebGenerationDeterministic`), and the rows ride the existing
retention cascade (`deleteReleaseRows`).

Unlike tar.gz, the deb bytes also bake in MUTABLE project state (control
`Description`/`Homepage`, the create_service postinst/prerm/unit members), so each
row's `metadata` records a fingerprint of exactly those inputs
(`{"inputs_sha256": ...}`, `debDigestFingerprint`) and a mismatch -- e.g. an
operator flipping create_service via the project PATCH, with no new release --
reads as a miss and refills the row in place, instead of leaving a stale digest
apt would reject every pool download against.

`Packages` and the `Release`/`InRelease` SHA256 lines are rendered by ONE shared
renderer (`packagesEntry` in `packages.go`, consumed by `computePackagesHashes` in
`release.go`), so the signed hashes always describe exactly the served index
bytes; a digest failure surfaces as a 500 on both routes rather than the pre-cache
silent fallback to the RAW upload's size/sha (values apt could never verify a
download against). With the cache warm, `Packages`/`Release`/`InRelease` requests
cost one DB read per architecture -- no repackaging.

## Package naming

The Debian package name is `repackage.DebPackageName(project.Name)` (folds `/` and
`_` to `-`, since neither is legal in a deb package name) -- the same value in the
served `Packages` index (`packages.go`), the InRelease hash computation
(`release.go`), the deb control `Package` field, the pool filename, and the
installed `/usr/bin/<pkg>` binary, so apt and dpkg always agree. A slash stays in
the repo *URL* (`apt.{domain}/<repo>/<binary>`), so `servePool` still resolves the
project from the request path; only the package *name* is folded (e.g.
`pr-reviewer-agent/server` installs as `pr-reviewer-agent-server`).

## Release signing

RSA 4096 key auto-generated on first startup, stored in
`BUILDHOST_DATA_DIR/apt-signing.key`. InRelease (clearsigned), Release.gpg
(detached), and key.asc (public key) endpoints are all served. Easiest client
setup is the generated per-project installer (`curl -fsSL
apt.{domain}/{project}/install.sh | sudo sh`): it saves the armored `key.asc` to
`/etc/apt/keyrings/buildhost-{project}.asc` and writes a `[signed-by=...]` source.
APT reads the armored key directly via `signed-by` (verified against apt 2.8), so
no `gpg --dearmor` step (or `gpg` binary) is required on the client. Private
projects pass a read token via `BUILDHOST_TOKEN`, which the installer also records
in `/etc/apt/auth.conf.d/` (scoped to the project's repo host+path).
