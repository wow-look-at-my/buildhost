# npm registry endpoint

`internal/npm/`. Extracted verbatim from CLAUDE.md, no wording changed.

npm registry endpoint on `npm.{domain}/@buildhost/{project}`. Tarball URLs point
to static. Self-registering via auth.OnReady().

A pre-built `kind=npm-package` artifact's packument reflects the uploaded
tarball's own `package.json` manifest (dependency graph + `bin`/`os`/`cpu`/
`engines`; `name`/`version`/`dist` stay buildhost-authoritative, lifecycle
`scripts` are never surfaced). Those fields are extracted lazily and cached per
artifact in `packaged_artifacts` under `format="npm-manifest"` -- uncached, a
packument decompressed EVERY published release's tarball and took 44s for 238
releases, which is what made the live registry look hung. Depth:
`docs/npm-packument-manifest-cache.md` (what is reflected and why, the cache
contract, cold-request bounds, measurements).

`redirect.go` also registers a main-domain (host-agnostic) route that
301-redirects the apex `/npm/*` to `npm.{domain}/*` (prefix stripped), analogous
to the `docker.{domain}` -> `oci.{domain}` redirect, so clients pointing an npm
registry base at `https://{apex}/npm/` (e.g. the go-toolchain action) reach the
npm subdomain. The redirect preserves the percent-encoded scope slash (`%2f`) via
`r.URL.EscapedPath()` + string concatenation so the npm `GET /{pkg}` single-segment
match still works.
