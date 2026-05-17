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
- `internal/auth/` - Token auth, OIDC JWT verification, middleware (EnforceProjectRead, RequireWrite)
- `internal/db/` - SQLite database layer (modernc.org/sqlite, no CGo), OIDC policy storage
- `internal/storage/` - Content-addressed blob storage (filesystem backend)
- `internal/repackage/` - Repackaging pipeline (tar.gz, tar.xz, tar.zst, zip, deb, brew, npm, oci)
- `internal/strip/` - Binary debug info stripping (shells out to strip/objcopy)
- `internal/model/` - Data types (Project, Release, Artifact, APIToken, OIDCPolicy)
- `internal/version/` - Version resolution logic
- `internal/config/` - Server configuration from env vars
- `migrations/` - SQLite schema (embedded via go:embed)

## Key design decisions

- Versioning: auto-increment (default) or semver (opt-in per project)
- Git branch is a first-class field on releases, not just metadata
- Repackaging happens eagerly at publish time, not on-the-fly
- Storage is content-addressed (SHA-256) for deduplication
- Auth: Bearer token, Basic auth, or query param — all resolve to the same token system
- OIDC: JWT-based auth for GitHub Actions (and any OIDC provider), verified via JWKS
- Private projects require auth on all endpoints including format-specific ones (APT, Brew, NPM, OCI)
- Tokens are project-scoped or global; project-scoped tokens cannot escalate privileges
- Token expiry is enforced at lookup time
- Upload size capped at 2 GiB

## First-time setup

```bash
buildhost bootstrap          # Creates initial admin token (only works when no tokens exist)
buildhost bootstrap --name admin-token
```

## Running

```bash
BUILDHOST_LISTEN_ADDR=:8080 BUILDHOST_BASE_URL=https://example.com buildhost serve
```

## OIDC for GitHub Actions

Configure an OIDC policy so GHA workflows can authenticate without static tokens:

```bash
# Create a policy that grants read,write to project ID 1 for any workflow in myorg/myrepo
curl -X POST https://buildhost.example.com/api/v1/oidc/policies \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"issuer":"https://token.actions.githubusercontent.com","subject_pattern":"repo:myorg/myrepo:*","project_id":1,"scopes":"read,write"}'
```

In the GHA workflow, request an OIDC token and use it as a Bearer token:

```yaml
permissions:
  id-token: write
steps:
  - uses: actions/github-script@v7
    id: token
    with:
      script: return await core.getIDToken('https://buildhost.example.com')
  - run: |
      curl -H "Authorization: Bearer ${{ steps.token.outputs.result }}" \
        https://buildhost.example.com/api/v1/projects
```

## Testing

`go-toolchain` runs all tests. Integration tests use httptest.NewServer with a temp SQLite DB.
OIDC tests generate ephemeral RSA keys and run a local JWKS server.
