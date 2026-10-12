# npm registry endpoint

`internal/npm/`. Extracted verbatim from CLAUDE.md, no wording changed.

npm registry endpoint on `npm.{domain}/@buildhost/{project}`. Tarball URLs point to static. Self-registering via init().

A pre-built `kind=npm-package` artifact's packument reflects the uploaded tarball's own `package.json` manifest. That means the dependency graph plus `bin`, `os`, `cpu` and `engines`. `name`, `version` and `dist` stay buildhost-authoritative, and a lifecycle `scripts` block never reaches the packument. Those fields are extracted lazily and cached per artifact in `packaged_artifacts` under `format="npm-manifest"`. Uncached, a packument decompressed EVERY published release's tarball. That took 44s for 238 releases, which is what made the live registry look hung. Depth: `docs/npm-packument-manifest-cache.md`, for what is reflected and why, the cache contract, the cold-request bounds and the measurements.

`redirect.go` also registers a host-agnostic main-domain route. It 301-redirects the apex `/npm/*` to `npm.{domain}/*` with the prefix stripped. It is the analogue of the `docker.{domain}` redirect to `oci.{domain}`. A client that points an npm registry base at `https://{apex}/npm/` therefore reaches the npm subdomain, the go-toolchain action included. The redirect preserves the percent-encoded scope slash (`%2f`) through `r.URL.EscapedPath()` and string concatenation. The npm `GET /{pkg}` single-segment match then still works.
