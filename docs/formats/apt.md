# APT repository endpoint

`internal/apt/`. Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed.

APT repository endpoint on `apt.{domain}/{project}/...`. A pool download redirects to static. It also serves the armored public signing key at `.../{project}/key.asc`. It serves a generated per-project `install.sh` one-liner installer (`install.go`), which adds the `signed-by` source and refreshes the index. The script's final `apt-get install` hint uses the folded deb package name, described below. Self-registering through init().

## Digest cache

The `Packages` index's per-artifact deb `Size` and `SHA256` are **cached in `packaged_artifacts` under `format="deb"`** (`debdigest.go`, `debDigest`). The fill is lazy on first need. After that it is a DB read, not a full ar and gzip repackage plus hash per artifact per request. It is the brew `tarGZSHA256` pattern.

The row is a digest cache only. `storage_key` records the SOURCE artifact blob. No deb is stored, and a pool download still repackages on demand. That is sound, because deb generation is deterministic per input set. The ar member headers are fixed with a zero timestamp, uid and gid. The tar mtimes are zero. The gzip header fields and the member order are fixed. The input is content-addressed. `TestDebGenerationDeterministic` pins all of it. The rows also ride the existing retention cascade (`deleteReleaseRows`).

The deb bytes, unlike a tar.gz, also bake in MUTABLE project state: the control `Description` and `Homepage`, and the create_service postinst, prerm and unit members. Each row's `metadata` therefore records a fingerprint of exactly those inputs (`{"inputs_sha256": ...}`, `debDigestFingerprint`). A mismatch reads as a miss and refills the row in place. An operator who flips create_service through the project PATCH, with no new release, causes one. Without that, the row keeps a stale digest, and apt rejects every pool download against it.

ONE shared renderer produces `Packages` and the `Release` and `InRelease` SHA256 lines. It is `packagesEntry` in `packages.go`, consumed by `computePackagesHashes` in `release.go`. The signed hashes therefore always describe exactly the served index bytes. A digest failure surfaces as a 500 on both routes. Before the cache it fell back silently to the RAW upload's size and sha, which are values apt can never verify a download against. With the cache warm, a `Packages`, `Release` or `InRelease` request costs one DB read per architecture, and no repackaging.

## Package naming

The Debian package name is `repackage.DebPackageName(project.Name)`. It folds `/` and `_` to `-`, because neither is legal in a deb package name. The same value appears in five places. They are the served `Packages` index (`packages.go`), the InRelease hash computation (`release.go`), the deb control `Package` field, the pool filename, and the installed `/usr/bin/<pkg>` binary. apt and dpkg therefore always agree. A slash stays in the repo *URL* (`apt.{domain}/<repo>/<binary>`). `servePool` still resolves the project from the request path. Only the package *name* folds, so `pr-reviewer-agent/server` installs as `pr-reviewer-agent-server`.

## Release signing

The key is Ed25519, which is OpenPGP EdDSA over Curve25519, algorithm 22. It is auto-generated on first startup and stored in `BUILDHOST_DATA_DIR/apt-signing.key`. Generation is one scalar multiply, about a millisecond. `TestNewSigner_GenerationIsWithinBudget` holds it under 100ms. An RSA-4096 prime search took seconds and made every caller work around the cost. Do not move to the newer `PubKeyAlgoEd25519`, algorithm 27. GnuPG 2.4's `gpgv` rejects it with "Invalid public key algorithm", and that is every client's `apt update`. An existing RSA key on disk is still loaded and used as it is. An upgrade therefore never rotates a deployment's key or invalidates a client's `signed-by`.

The InRelease (clearsigned), Release.gpg (detached) and key.asc (public key) endpoints are all served. The easiest client setup is the generated per-project installer (`curl -fsSL apt.{domain}/{project}/install.sh | sudo sh`). It saves the armored `key.asc` to `/etc/apt/keyrings/buildhost-{project}.asc`. It writes a `[signed-by=...]` source. APT reads the armored key directly through `signed-by`, verified against apt 2.8. The client therefore needs no `gpg --dearmor` step and no `gpg` binary. A private project passes a read token through `BUILDHOST_TOKEN`. The installer also records that token in `/etc/apt/auth.conf.d/`, scoped to the project's repo host and path.
