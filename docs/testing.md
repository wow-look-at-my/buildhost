# Testing

Extracted verbatim from CLAUDE.md, no wording changed.

`go-toolchain` runs all tests. Integration tests use httptest.NewServer with a temp SQLite DB. OIDC tests generate ephemeral RSA keys and run a local JWKS server.

## Inline action scripts must carry no stacked comments

`.github/scripts/no-stacked-comments.ts` runs from `test/dats/repo-hygiene.dats`. It refuses two or more consecutive comment-only `//` lines inside an inline `script:`. It covers any `.github/actions/*/action.yml` and any workflow.

`wow-look-at-my/actions@typescript#latest` enforces the same rule at RUN time, with no opt-out. Most of these actions execute on only some triggers. Without a repo-level check a violation therefore ships. It then surfaces as a broken publish in whichever consumer runs it next. A shell `run:` block and a checked-in `file:` script are exempt, exactly as they are for the action.

The remedy is the one the action names: say it in a single line, or move the prose to `docs/` and leave a pointer.

## llms.txt drift guard

`internal/server/llms_endpoints_test.go` guards the `/llms.txt` document against drift. It parses the *served* document. It asserts that every URL the document references resolves to a registered route. It then exercises the documented flows end to end against a seeded server. Those flows are the downloads, APT, Brew, npm, OCI, and the `/static` latest-rejection. An edit to `internal/llms/template.md` that references a nonexistent endpoint fails CI.

## synthesized-image-e2e

`test/dats/synthesized-image.dats` is a synthesized-OCI-image end-to-end test. CI job `synthesized-image-e2e` in `ci.yml` runs it, and `go-toolchain` does not.

It publishes a tiny static binary to a real `buildhost serve`. That binary is `test/e2e/testdata/netcheck/`. It sits under `testdata/`, so `go list ./...` ignores it. go-toolchain's build, vet and coverage therefore ignore it too. The e2e job still builds it explicitly.

The suite then uses **crane** (go-containerregistry) to pull the image buildhost synthesizes. It asserts the config, which covers the entrypoint, `SSL_CERT_FILE`, and two ordered `diff_ids`. It asserts the flattened rootfs, which covers the CA bundle, `nonroot` in `/etc/passwd`, and a sticky `/tmp`. It then runs the entrypoint. The entrypoint makes an outbound HTTPS request validated **only** against the image's baked-in CA bundle. That proves a networked service works in the synthesized image.

Docker pulls and runs the same image too. "Pullable" is a claim about the client people actually use. buildhost's layers are `tar+zstd`. Docker reads that only through the containerd image store, which the workflow turns on. crane reads it anywhere, with no daemon.

## homebrew-tap-e2e

CI job `homebrew-tap-e2e` (`ci.yml`) runs the README-documented brew flows against a real spawned buildhost. The commands come from `scripts/brew-doc-flows.sh`. That script extracts the fenced blocks from `README.md` and substitutes only the host.

`test/dats/homebrew-public.dats` executes the public flow. It asserts that the served `/llms.txt` documents the same blocks. `homebrew-private.dats` executes the private flow and checks the authenticated tap. `homebrew-anon-leak.dats` clones the anonymous tap and probes the unauthorized formula paths.

What the job INSTALLS is the real thing. The public `go-toolchain` project is seeded from the live registry's own artifact, at `dl.pazer.build/go-toolchain?branch=v1&os=&arch=`. That download is public and needs no token. It comes from the same registry the `build` job's go-toolchain action already depends on. The job therefore asserts the actual user-facing claim: `brew install pazer/build/go-toolchain` yields a runnable binary. It runs `go-toolchain version`, which exits 0 even when its update check cannot reach GitHub.

The fixture's FILE TYPE is the whole point. Homebrew's Cleaner decides an installed file's mode from it. The previous `#!/bin/sh` stand-in was classified executable whatever happened. No formula regression was therefore able to turn the job red. That is how a `0444`, unrunnable `brew install go-toolchain` shipped while CI stayed green.

A second project, `ape-fixture`, is a synthetic APE SHAPE. It carries no `#!`. It is neither an ELF nor a Mach-O. It appends to `$0` before it prints its marker, which stands in for self-assimilation, so a `0555` install fails too.

go-toolchain ships an APE only on Linux. Its darwin artifacts are plain Mach-O, which brew recognizes. The fixture therefore keeps the mode invariant covered on the macOS runner, and for any publisher that ships a Cosmopolitan binary. Both installs assert `-x` AND `-w` on the installed file before they execute it.

To run the REAL binary is also what surfaced the strip corruption above. A GitHub runner has binutils. The unfixed server therefore served a mangled tarball whose sha256 never matched the formula.

## container-healthcheck

CI job `container-healthcheck` (`ci.yml`) additionally runs `test/dats/image-strips.dats` against the **built Docker image**. It bootstraps a token by an exec of the binary inside the container. It publishes buildhost's own unstripped linux binary.

It then asserts that the download comes back smaller than the upload. The download must carry no `.symtab` section and no `.debug_*` section. It must keep `.text` and `.rodata` intact. It asserts that `fmt=symbols` returns a file with a real `.debug_info` section. It asserts that `?debug=1` returns exactly the uploaded bytes.

A unit test can never catch the defect this guards. The runner has binutils and the container does not. That is precisely why stripping was broken in production while CI stayed green.

## The preview dashboard's links

`test/dats/admin-demo-links.dats` is a step in `sites-cors-e2e`. It serves the built `internal/admin/static` under a path prefix, which is what puts the SPA in demo mode. It then walks every `#/` link the SPA renders, breadth-first, from the dashboard outward. A page must draw a heading that is not the error page.

It exists because the demo dataset linked to a page it had no fixture for. The missing fixture resolved to `{}`. The renderer threw on that value, and the previous page stayed on screen. The link therefore did nothing. `apiFetch` now throws on an unknown demo path, and the router paints the failure. The same defect is therefore loud instead of invisible. The crawl fails on it either way.

## apt-install-e2e

`test/dats/apt-install.dats` (CI job `apt-install-e2e`) covers a third case beyond the plain and slash-namespaced packages: an **APE-shaped artifact** (no shebang, not an ELF, and it writes to `$0` before printing its marker). The generated package must install the binary under `/usr/lib`, with a `/bin/sh` launcher on `$PATH`. The suite then runs it **as a non-root user**, which is the exact case that failed. It asserts the marker output and a writable per-user copy. The suite is verified to go red without the deb fix.

The apt client is a container, built from `test/dats/aptbox.Dockerfile` and started by the workflow in their own untimed steps. The suite installed onto the runner before this. It therefore inherited that host's apt state, and its setup hook paid for the base packages. The image bakes curl, gnupg, systemd and the non-root `aptuser`.

The container runs on the default bridge. `host-gateway` points the `apt.localhost` and `static.localhost` entries in its hosts file at the runner. Apt reads that file and needs nothing else. Curl does not read it. Curl pins a `*.localhost` name to loopback, because that name is reserved for it. The key fetch therefore passes `--resolve`, the documented override. The suite reads the address out of the container rather than assuming one.

The scheme is why these names stay under `.localhost`. `auth.RequestScheme` answers http for a loopback name. It answers https for every other name. Any other domain therefore makes the generated package URL https, and apt then fails to connect.

## upload-artifact-action-e2e

CI job `upload-artifact-action-e2e` (`ci.yml`) exercises the `.github/actions/buildhost-upload-artifact` composite. It runs against a real spawned buildhost whose advertised `max_direct_upload_bytes` is shrunk to 1 MiB.

A small file must upload as one classic direct PUT. The server log must show no session-endpoint traffic. A file of about 3 MiB must assemble through a chunked upload session, with one create and four 1 MiB `?offset=` PATCH requests. Each server-computed sha256 must equal the local file's own. The chunk-assembled artifact must download back byte-identical. The create-release and publish-release composites run as part of the flow.

The same job runs single-mode `buildhost publish`, with no `--manifest`, against the real binary. It then asserts that the project's apex `latest` resolves published. The CLI package deliberately has no unit test. Any such test pulls the whole untested `cmd/buildhost` into the coverage denominator. See `internal/uploadclient`'s doc comment. CLI behavior is therefore guarded here, as `--manifest` mode is guarded in `homebrew-tap-e2e`.

The job runs the composites, because an action runs only inside a workflow. Every assertion above lives in a suite the job invokes with the results. `test/dats/upload-direct.dats` asserts that the small upload opened no session. `test/dats/upload-artifact.dats` asserts the chunked session, the hashes, the byte-identical download, and the published release. `test/dats/site-direct.dats` asserts that the small site opened no session either. `test/dats/site-publish.dats` asserts the big site's own session. It also asserts the advertised URL that serves with no `/branch/` in it, the served bytes, the legacy redirect, and the single-mode CLI publish.

## test/dats/multi-platform-ape.dats

`test/dats/multi-platform-ape.dats` runs the real binary. It publishes ONE APE covering `linux/amd64,darwin/arm64,windows/amd64` through `PUT .../artifacts/ape`.

It then asserts that the release holds exactly one artifact. Every request spelling, `macOS/aarch64` included, must get the SAME `dl` redirect target and the same sha256 back. The release page must render one `raw` link with an `APE: …` badge. A non-APE multi-platform claim must be a 400 that stores nothing.

The Go tests cover the same properties in process. This suite exists because a redirect that resolves per platform instead of per artifact still passes every in-process assertion about bytes. The defect shows only as one URL becoming several. That needs the real `dl` handler behind a real Host header.

Each test starts its own server. It takes the binary from `BUILDHOST_BIN`, or else from `build/buildhost`, because a cosmo build writes the APE under that plain name. It starts an APE through a shell, because the kernel cannot exec one.

It drives that server with curl and jq. The runner has both. The docker image that dats sandboxes into does not. That is why the suite runs `--no-sandbox`, and why it lives in `test/dats/` rather than `dats/`. Everything under `dats/` runs sandboxed on every build. Locally, `dats test/dats/multi-platform-ape.dats` sandboxes fine under bwrap, which binds the host's own tools. Depth: `docs/multi-platform-artifacts.md`.

## Where a test lives

An assertion goes in a dats suite, never in a workflow step. `test/dats/` is where those suites live. A workflow step invokes one by name with `--no-sandbox`. These suites need the host: brew and its prefix, curl and jq, and a service the workflow started first. The sandbox dats falls back to on a runner is a bare `debian:stable-slim` with none of that.

`dats/` at the module root is the other option. It is currently empty. `go-toolchain` walks it on every build and runs it sandboxed. A suite there may therefore need only what that image has.

A workflow step may still DO things: install brew, start a server, run a composite action. What it may not do is hold the expected value.

Some checks need a program rather than a shell: octokit fakes, a browser crawl, a manual walk of a redirect chain. Those live under `test/actions/` as node tests -- node runs TypeScript directly -- and a dats suite invokes each one and reads its output. `action-libs.dats` covers the storage-record module, `admin-demo-links.dats` the preview crawl, and `sites-cors.dats` the cross-origin import.

## The route table golden (docs/routes.txt)

`docs/routes.txt` is the committed route table, rendered by the program itself (`auth.AllRoutes()`, the same enumeration `buildhost routes` prints) and never parsed out of source. Two gates keep it honest, both fail-on-drift:

- `internal/routescheck/golden_test.go` fails the ordinary build when the route set differs from the file, naming the regeneration command.
- The `route-diff` CI job re-checks the file against the REAL BINARY's `routes` output. The golden can therefore never describe a route the shipped program does not serve.

Regenerate with `go-toolchain && ./build/buildhost routes > docs/routes.txt`, or `UPDATE_ROUTES_GOLDEN=1 go-toolchain` when no binary is built yet.

It exists because this repo has no central router file. Every backend self-registers from its own `init()`. Before the golden, an added endpoint therefore left nothing route-shaped in Files Changed for a reviewer to look at. The golden turns a new route into an ordinary one-line diff. It also makes a duplicated or unintended route impossible to land unnoticed.

`internal/routescheck/routes_test.go` guards the mechanism the golden depends on: routes must register in `init()`, not inside an `auth.OnReady()` callback. OnReady fires only from `auth.Init()` at server boot, so a route registered there is invisible to `buildhost routes`, to the golden, and to the route-diff check. Its `want` list covers every backend including `/api/v1`.
