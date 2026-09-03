# Embedded CA certificate bundle

`internal/repackage/oci.go` embeds `cacerts/ca-certificates.crt` (`//go:embed`) and writes
it into the shared "essentials" layer of every OCI image buildhost synthesizes from a raw
binary, at `/etc/ssl/certs/ca-certificates.crt` (Go's default Linux x509 path), so a
networked binary can make outbound HTTPS calls.

## The bundle is fetched at build time, not committed

`ca-certificates.crt` is **gitignored** — it is downloaded during the build from a
trustworthy upstream (the Mozilla CA bundle as published by curl) rather than vendored
into the repo. That keeps a large, unreviewable cert blob out of git history and out of
PR diffs, and means the bundle is always whatever upstream currently ships.

## Fetching it is a generate directive

Being fetched rather than committed makes the bundle a generated build input, exactly like
`internal/api`'s `gen_*.go`, so it is produced the same way — a `//go:generate` directive
next to the `//go:embed` in `oci.go` runs `scripts/fetch-cacerts.sh`. Nothing has to
remember a separate step:

```sh
go-toolchain --generate 0af38e6ba5f6   # runs every directive, this one included
./scripts/fetch-cacerts.sh             # or fetch it alone
```

(The hash is go-toolchain's approval gate over the directive set; it prints the current
one if the recorded value is stale.) Skip generate in a fresh clone and the build fails
with `pattern cacerts/ca-certificates.crt: no matching files found` — the embed is
compile-time on purpose, so a bundle-less binary can never ship. The source URL can be
overridden with `CACERT_URL`. `internal/repackage` has `TestCACertsBundleValid`, which
fails CI if the fetched bundle is empty or unparseable.
