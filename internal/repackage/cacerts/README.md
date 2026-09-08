# Embedded CA certificate bundle

`internal/repackage/oci.go` embeds `cacerts/ca-certificates.crt` with `//go:embed`. It writes the bundle into the shared "essentials" layer of every OCI image buildhost synthesizes from a raw binary. The path is `/etc/ssl/certs/ca-certificates.crt`, Go's default Linux x509 path. A networked binary can then make outbound HTTPS calls.

## The bundle is fetched at build time, not committed

`ca-certificates.crt` is **gitignored**. The build downloads it from a trustworthy upstream, the Mozilla CA bundle as published by curl, instead of a vendored copy in the repo. That keeps a large, unreviewable cert blob out of git history and out of PR diffs. It also makes the bundle whatever upstream ships today.

## Fetching it is a generate directive

A fetched bundle is a generated build input, exactly like `internal/api`'s `gen_*.go`. It is produced the same way. A `//go:generate` directive next to the `//go:embed` in `oci.go` runs `scripts/fetch-cacerts.sh`. Nobody has to remember a separate step:

```sh
go-toolchain --generate 5f3f7e6c9924   # runs every directive, this one included
./scripts/fetch-cacerts.sh             # or fetch it alone
```

The hash is go-toolchain's approval gate over the directive set. It prints the current one when the recorded value is stale. Skip generate in a fresh clone and the build fails with `pattern cacerts/ca-certificates.crt: no matching files found`. The embed is compile-time on purpose, so a bundle-less binary can never ship. `CACERT_URL` overrides the source URL. `internal/repackage` has `TestCACertsBundleValid`, which fails CI when the fetched bundle is empty or unparseable.
