# buildhost

Universal package registry server. Upload artifacts once, download in any format.

## Build

```bash
go-toolchain
```

This runs mod tidy, vet, tests with coverage, and builds the binary. Do not use bare `go` commands.

## Project structure

- `cmd/buildhost/` - CLI entrypoint (cobra, one subcommand per file, self-registering via init())
- `internal/server/` - HTTP server, routing, middleware
- `internal/api/` - REST API handlers (projects, releases, artifacts, publish, tokens)
- `internal/dl/` - Download handlers with version/branch resolution
- `internal/apt/` - APT repository endpoint
- `internal/brew/` - Homebrew tap endpoint
- `internal/npm/` - npm registry endpoint
- `internal/oci/` - OCI distribution endpoint
- `internal/auth/` - Token auth, OIDC, middleware
- `internal/db/` - SQLite database layer (modernc.org/sqlite, no CGo)
- `internal/storage/` - Content-addressed blob storage (filesystem backend)
- `internal/repackage/` - Repackaging pipeline (tar.gz, tar.xz, tar.zst, zip, deb, brew, npm, oci)
- `internal/strip/` - Binary debug info stripping (shells out to strip/objcopy)
- `internal/model/` - Data types (Project, Release, Artifact, APIToken)
- `internal/version/` - Version resolution logic
- `internal/config/` - Server configuration from env vars
- `migrations/` - SQLite schema (embedded via go:embed)

## Key design decisions

- Versioning: auto-increment (default) or semver (opt-in per project)
- Git branch is a first-class field on releases, not just metadata
- Repackaging happens eagerly at publish time, not on-the-fly
- Storage is content-addressed (SHA-256) for deduplication
- Auth: Bearer token, Basic auth, or query param — all resolve to the same token system
- Private projects require auth on all endpoints including format-specific ones

## Running

```bash
BUILDHOST_LISTEN_ADDR=:8080 BUILDHOST_BASE_URL=https://example.com buildhost serve
```

## Testing

`go-toolchain` runs all tests. Integration tests use httptest.NewServer with a temp SQLite DB.
