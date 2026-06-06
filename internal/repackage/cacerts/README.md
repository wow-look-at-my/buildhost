# Embedded CA certificate bundle

`ca-certificates.crt` is the Mozilla CA root bundle as published by the curl project. It
is `//go:embed`-ed by `internal/repackage/oci.go` and placed at
`/etc/ssl/certs/ca-certificates.crt` (Go's default Linux x509 path) in the shared
"essentials" base layer of every OCI image synthesized from a raw binary, so a networked
binary can make outbound HTTPS calls out of the box.

## Provenance

- Source: <https://curl.se/ca/cacert.pem> (extracted from Mozilla NSS `certdata.txt`).
- Format: concatenated PEM `CERTIFICATE` blocks.

## Refreshing

```sh
curl -fsSL -o internal/repackage/cacerts/ca-certificates.crt https://curl.se/ca/cacert.pem
```

`internal/repackage` has a test (`TestCACertsBundleValid`) asserting the bundle parses to
at least one certificate, so a truncated or empty file fails CI. Refreshing the bundle
changes the synthesized base-layer digest; that is expected (digests are regenerated on
demand, not pinned).
