# Testing

Extracted verbatim from CLAUDE.md, no wording changed.

`go-toolchain` runs all tests. Integration tests use httptest.NewServer with a
temp SQLite DB. OIDC tests generate ephemeral RSA keys and run a local JWKS
server.

## Inline action scripts must carry no stacked comments

`.github/scripts/no-stacked-comments.ts` (a `build` step) refuses two or more
consecutive comment-only `//` lines inside an inline `script:` in any
`.github/actions/*/action.yml` or workflow. That is the rule
`wow-look-at-my/actions@typescript#latest` enforces at RUN time, with no opt-out
— but most of these actions only execute on some triggers, so without a
repo-level check a violation ships and surfaces as a broken publish in whichever
consumer runs it next. A shell `run:` block and a checked-in `file:` script are
exempt, exactly as they are for the action.

The remedy is the one the action names: say it in a single line, or move the
prose to `docs/` and leave a pointer.

## llms.txt drift guard

`internal/server/llms_endpoints_test.go` guards the `/llms.txt` document against
drift: it parses the *served* document and asserts every URL it references
resolves to a registered route, then exercises the documented flows (downloads,
APT/Brew/npm/OCI, the `/static` latest-rejection) end to end against a seeded
server. Editing `internal/llms/template.md` to reference a nonexistent endpoint
fails CI.

## synthesized-image-e2e

`test/e2e/` is a synthesized-OCI-image end-to-end test (CI job
`synthesized-image-e2e` in `ci.yml`, not part of `go-toolchain`). It publishes a
tiny static binary (`test/e2e/testdata/netcheck/`; under `testdata/` so `go list
./...` -- and thus go-toolchain's build/vet/coverage -- ignores it, while the e2e
job still builds it explicitly) to a real `buildhost serve`, then uses **crane**
(go-containerregistry) to pull the image buildhost synthesizes, assert its config
(entrypoint, `SSL_CERT_FILE`, two ordered `diff_ids`) and flattened rootfs (CA
bundle, `nonroot` in `/etc/passwd`, sticky `/tmp`), and run the entrypoint --
which does an outbound HTTPS request validated **only** against the image's
baked-in CA bundle, proving a networked service works in the synthesized image.
crane (not docker) because buildhost's layers are `tar+zstd`, which
go-containerregistry pulls but the default GitHub Docker may not.

## homebrew-tap-e2e

CI job `homebrew-tap-e2e` (`ci.yml`) runs the README-documented brew flows against
a real spawned buildhost, and what it INSTALLS is now the real thing: the public
`go-toolchain` project is seeded by downloading the live registry's own artifact
(`dl.pazer.build/go-toolchain?branch=v1&os=&arch=`, public, no token -- the same
registry the `build` job's go-toolchain action already depends on), so the job
asserts the actual user-facing claim that `brew install pazer/build/go-toolchain`
yields a runnable binary (`go-toolchain version`, which exits 0 even when its
update check can't reach GitHub).

The fixture's FILE TYPE is the whole point: Homebrew's Cleaner decides an
installed file's mode from it, and the previous `#!/bin/sh` stand-in was
classified executable no matter what, so no formula regression could ever turn the
job red -- that is how a `0444`, unrunnable `brew install go-toolchain` shipped
while CI stayed green. A second project, `ape-fixture`, is a synthetic APE SHAPE
(no `#!`, not an ELF/Mach-O, and it appends to `$0` before printing its marker,
standing in for self-assimilation so a `0555` install fails too): go-toolchain
ships an APE only on Linux -- its darwin artifacts are plain Mach-O, which brew
recognizes -- so this keeps the mode invariant covered on the macOS runner and for
any publisher shipping a Cosmopolitan binary. Both installs assert `-x` AND `-w`
on the installed file before executing it. Running the REAL binary is also what
surfaced the strip corruption above: GitHub runners have binutils, so the unfixed
server served a mangled tarball whose sha256 never matched the formula.

## container-healthcheck

CI job `container-healthcheck` (`ci.yml`) additionally runs
`.github/scripts/image-strips-e2e.ts` against the **built Docker image**: it
bootstraps a token by exec'ing the binary inside the (shell-less) container,
publishes buildhost's own unstripped linux binary, and asserts the download comes
back smaller than the upload with no `.symtab`/`.debug_*` sections but
`.text`/`.rodata` intact, that `fmt=symbols` returns a file with a real
`.debug_info` section, and that `?debug=1` returns exactly the uploaded bytes.
Unit tests could never catch the defect this guards -- the runner has binutils and
the container does not -- which is precisely why stripping was broken in
production for weeks with CI green.

## apt-install-e2e

`test/e2e/apt-install.sh` (CI job `apt-install-e2e`) covers a third case beyond
the plain and slash-namespaced packages: an **APE-shaped artifact** (no shebang,
not an ELF, and it writes to `$0` before printing its marker). The generated
package must install the binary under `/usr/lib` with a `/bin/sh` launcher on
`$PATH`, and the script then runs it **as the non-root CI user** -- the exact case
that failed -- asserting the marker output and a writable per-user copy. Verified
to go red without the deb fix.

## upload-artifact-action-e2e

CI job `upload-artifact-action-e2e` (`ci.yml`) exercises the
`.github/actions/buildhost-upload-artifact` composite against a real spawned
buildhost whose advertised `max_direct_upload_bytes` is shrunk to 1 MiB: a small
file must upload as one classic direct PUT (the server log must show no
session-endpoint traffic), a ~3 MiB file must assemble through a chunked upload
session (exactly one create and four 1 MiB `?offset=` PATCHes), both
server-computed sha256s must equal the local files', and the chunk-assembled
artifact must download back byte-identical -- with the create-release and
publish-release composites running as part of the flow. The same job runs
single-mode `buildhost publish` (no `--manifest`) with the real binary and asserts
the project's apex `latest` resolves published afterward; the CLI package
deliberately has no unit tests (adding any would pull the whole untested
`cmd/buildhost` into the coverage denominator -- see `internal/uploadclient`'s doc
comment), so CLI behavior is guarded here, like `--manifest` mode is in
`homebrew-tap-e2e`.

## dats/multi-platform-ape.dats

`dats/multi-platform-ape.dats` runs the real binary and publishes ONE APE
covering `linux/amd64,darwin/arm64,windows/amd64` through
`PUT .../artifacts/ape`, then asserts the release holds exactly one artifact,
that four request spellings (including `macOS/aarch64`) all get the SAME `dl`
redirect target and the same sha256 back, that the release page renders one
`raw` link with an `APE: …` badge, and that a non-APE multi-platform claim is a
400 storing nothing.

The Go tests cover the same properties in process. This suite exists because a
redirect that resolves per platform instead of per artifact still passes every
in-process assertion about bytes -- the defect only shows as one URL becoming
three, which needs the real `dl` handler behind a real Host header.

Each test starts its own server inside dats' sandbox, so the suite needs no CI
job of its own: `go-toolchain` runs it on every build, and `dats
dats/multi-platform-ape.dats` runs it from a checkout that has built a binary.
It takes the binary from `BUILDHOST_BIN`, else whichever of
`build/buildhost_cosmo_fat` or `build/buildhost` the build left. Depth:
`docs/multi-platform-artifacts.md`.

## The route table golden (docs/routes.txt)

`docs/routes.txt` is the committed route table, rendered by the program itself
(`auth.AllRoutes()`, the same enumeration `buildhost routes` prints) and never
parsed out of source. Two gates keep it honest, both fail-on-drift:

- `internal/routescheck/golden_test.go` fails the ordinary build when the route
  set differs from the file, naming the regeneration command.
- The `route-diff` CI job re-checks the file against the REAL BINARY's `routes`
  output, so the golden can never describe routes the shipped program does not
  serve.

Regenerate with `go-toolchain && ./build/buildhost routes > docs/routes.txt`,
or `UPDATE_ROUTES_GOLDEN=1 go-toolchain` when no binary is built yet.

It exists because this repo has no central router file -- every backend
self-registers from its own `init()` -- so before the golden, adding an endpoint
left nothing route-shaped in Files Changed for a reviewer to look at. The golden
turns a new route into an ordinary one-line diff, and makes a duplicated or
unintended route impossible to land unnoticed.

`internal/routescheck/routes_test.go` guards the mechanism the golden depends
on: routes must register in `init()`, not inside an `auth.OnReady()` callback.
OnReady fires only from `auth.Init()` at server boot, so a route registered
there is invisible to `buildhost routes`, to the golden, and to the route-diff
check. Its `want` list covers every backend including `/api/v1`.
