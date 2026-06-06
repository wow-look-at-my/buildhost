// Standalone module (nested, so it is excluded from the buildhost module's ./...
// build, tests and coverage). It is the entrypoint binary for the synthesized-OCI
// end-to-end test: it proves the CA bundle baked into the image validates a real
// public TLS handshake. Stdlib only -- no go.sum.
module buildhost-e2e-netcheck

go 1.25
