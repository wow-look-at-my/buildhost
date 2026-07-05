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

Fetch it before building (CI does this automatically in the `build` job):

```sh
./scripts/fetch-cacerts.sh
```

`go build` / `go-toolchain` will fail with a "no matching files" embed error if the
bundle has not been fetched yet — run the script first. The source URL can be overridden
with `CACERT_URL`. `internal/repackage` has `TestCACertsBundleValid`, which fails CI if
the fetched bundle is empty or unparseable.
