# Go module proxy

`internal/goproxy/`. Serves the [Go module download protocol][protocol] on `goproxy.{domain}`, replacing a separately-deployed [Athens][athens].

[protocol]: https://go.dev/ref/mod#goproxy-protocol [athens]: https://github.com/gomods/athens

## What replaced what, and why

The Athens deployment answered `404` with an empty body for every private first-party module while serving public ones from the same org perfectly. The upstream cause was visible only on `@v/list`, which alone returned a body:

```
not found: module github.com/wow-look-at-my/tml: git ls-remote -q
https://github.com/wow-look-at-my/tml in /tmp/athens399040138/...: exit status 128:
        fatal: could not read Username for 'https://github.com': terminal prompts disabled
```

That is not an unapproved token. It is **no credential at all**. Athens' `ATHENS_GITHUB_TOKEN` was unset. The `git` subprocess it shells out to therefore had nothing to present. It prompted for a username and died. Two defects compounded:

1. **A subprocess turns a credential failure into an opaque exit code.** The real error sat several layers down, inside a child process's stderr.
2. **The failure was reported as `404`.** At the protocol level that means "this module does not exist". `go mod download` therefore reports a missing module. The reader then looks for a typo in `go.mod` instead of at the credential.

Both are design properties here, not bug fixes:

- **No subprocess.** Content comes from the GitHub REST API and the public mirror over `net/http`. Every failure is an HTTP status, classified in `errors.go`.
- **Only a genuine absence may be a `404`.** `Kind` decides the status, in one place, and `asError` classifies an unrecognized error as `KindUpstream` rather than guessing absence.

## Status mapping

| Kind | Status | Means |
| --- | --- | --- |
| `not_found` | 404 | Upstream was readable and the module is genuinely absent. |
| `unauthorized` | 403 | The proxy's own credential was rejected, or it has none. |
| `upstream` | 502 | Upstream failed, was unreachable, or rate-limited. |
| `invalid_request` | 400 | Not a well-formed module-proxy request. |

The response body is the whole diagnosis. `go mod download` prints a proxy's body verbatim. The body therefore names the module, the version, the upstream and its status. For an authorization failure it says outright that this is not a missing module.

Two ambiguities are resolved deliberately:

- **A GitHub `404` with no credential is `unauthorized`, not `not_found`.** GitHub does not confirm to an unauthorized caller that a private repository exists. With no credential the two cases are therefore strictly indistinguishable. To report absence is the exact laundering described above. With a credential present, a `404` is reported as `not_found`. The detail then says the credential was presented. A reader can therefore tell an access problem from a real absence.
- **A rate-limit `403` is `upstream`, not `unauthorized`.** It clears on its own. To report it as an authorization failure sends the reader after a credential that was never at fault. It is detected from `X-RateLimit-Remaining: 0`, from `Retry-After`, or from GitHub's own message.

## Readiness

A module proxy with no credential serves every public module and none of the private ones. Nothing that only asks "is the process up" can see that, which is why it went unnoticed. So readiness is its own statement, re-checked every 15 minutes, reported on the admin dashboard and at `goproxy.{domain}/health` (503 when not ready), and logged at ERROR at startup.

It reports three distinct states:

- **Not ready** -- no credential while private prefixes are configured, or the configured readiness module did not resolve.
- **Ready but unproven** -- a credential exists, and no `BUILDHOST_GOPROXY_READINESS_MODULE` is set. A credential that authenticates but is not authorized for the org looks identical to a working one from here. The check therefore says so, rather than a claim of a proof it cannot make. **Set a private module here.** It is the only configuration that catches the failure class this package exists for.
- **Ready** -- the readiness module resolved.

The endpoint splits what it tells whom. The status code and the `healthy` flag are unauthenticated. A monitor therefore needs no credential. A monitor that needs one is a monitor nobody wires up.

The reason, the credential state, the private prefixes and the readiness module are served only to a read-scoped caller. Each of them names a private repository. A private module's existence is not something an anonymous caller may learn. The admin dashboard reads the full state directly, on the admin port, so nothing is hidden from an operator.

It deliberately does NOT fail the registry's `/healthz`. A goproxy misconfiguration then takes every other buildhost service out of rotation. That outcome is worse than the one this check prevents.

## Configuration

| Env var | Default | Meaning |
| --- | --- | --- |
| `BUILDHOST_GOPROXY_PRIVATE_PREFIXES` | `github.com/<org>` per `BUILDHOST_OIDC_ORGS` | Module prefixes fetched direct from GitHub with buildhost's credential. |
| `BUILDHOST_GOPROXY_UPSTREAM` | (unset) | Optional mirror to forward non-private modules to. Off by default. |
| `BUILDHOST_GOPROXY_READINESS_MODULE` | (unset) | A PRIVATE module resolved at startup to prove the credential works. |

The credential is buildhost's existing one -- `BUILDHOST_GITHUB_APP_ID` + `BUILDHOST_GITHUB_APP_PRIVATE_KEY`, else `BUILDHOST_GITHUB_TOKEN` (see `docs/running.md`). There is no goproxy-specific credential: a second one is a second thing to leave unset. A GitHub App installed on the org with `contents: read` is the right answer, and avoids the fine-grained-PAT org-approval trap entirely.

Private prefixes default to the configured OIDC orgs, so a deployment that already declares which orgs it serves needs no extra configuration.

## No third-party mirror by default

`BUILDHOST_GOPROXY_UPSTREAM` is unset out of the box. buildhost never picks a mirror for you. A module mirror sees the path of every dependency routed through it. A default mirror therefore hands a third party the org's entire dependency graph. That graph includes the path of any private module whose prefix is absent from `BUILDHOST_GOPROXY_PRIVATE_PREFIXES`. A leak is worst and least visible in that case. Athens, which this replaces, ran `GOPROXY=direct` for the same reason. A default mirror here is a regression, not a port.

A module this proxy does not serve is answered **404**, which in a `GOPROXY` list is the protocol's "try the next entry":

```
GOPROXY=https://goproxy.pazer.build,direct
```

Everything outside the org is then fetched straight from its origin, exactly as `GOPROXY=direct` did. This is measured, not assumed. `go` advances to the next entry on a 404 and on a 410. It halts on any other status, such as a 403 or a 502. That is why an authorization failure stays a 403. A credential problem must halt and must be reported. It must never fall through to `direct` and quietly succeed while the proxy is misconfigured.

An operator who does want a mirror sets `BUILDHOST_GOPROXY_UPSTREAM` explicitly, and can point it at a self-hosted one.

## Auth

Two different credentials meet here, and confusing them is what produced the bug this package was written for. The PROXY's credential (a GitHub App installation token, else the static PAT, resolved per repo by `auth.BearerForRepo`) decides what the proxy can fetch. The CALLER's credential decides what they may see.

A module outside the private namespaces is public source and needs no credential. To require one only stops `GOPROXY=<proxy>,direct` from working for anyone without a buildhost token.

Inside those namespaces a caller needs one of two things. The first is a GLOBAL read or write token. The second is a signed-in GitHub user who can read the backing repo, which is asked of GitHub per repo. A project-scoped token is not enough. It says "this job may read project X". A Go module is not a project. To accept it widens a least-privilege credential to the org's whole private source tree.

A caller without that access gets **404**, never a 401 and never a 403. That is the same answer a module that does not exist gets. Either of the other two confirms the module EXISTS, which is the fact a private module is keeping. A prober can then walk a name list and map the org's private repositories off the status code alone.

The check runs before any upstream call. The existing module and the fictional one therefore cannot drift apart in timing or in body. An unauthenticated request also never spends GitHub API quota. `TestExistingAndMissingPrivateModulesAreIndistinguishable` holds the property.

This is the exact opposite of `KindUnauthorized`. The two must never merge. There the proxy's OWN credential failed. No caller can fix that, and a 404 buries it. That is how a proxy that served zero private modules looked like a typo in everybody's `go.mod`. That case stays a loud 403.

Point the toolchain at it with `~/.netrc`:

```
machine goproxy.pazer.build login x password <read-scoped token>
```

## Module resolution

Module paths map onto GitHub repositories in `modpath.go`:

- `github.com/o/r` -> repo root, tags `vX.Y.Z`.
- `github.com/o/r/sub` -> the `sub` directory, tags `sub/vX.Y.Z`. This is a real shape in the org (`agentic-loop/go`), and getting it wrong makes the module unresolvable.
- `github.com/o/r/v2` -> either the repo root (whose `go.mod` declares the `/v2` path) or a `v2` subdirectory. Both are candidates, and the `go.mod` that actually declares the requested path settles it.

`@latest` resolves to the highest release version. It falls back to the highest pre-release, and then to a **pseudo-version of the default branch head**. That last case is the normal one for this org's untagged, branch-pinned first-party modules. It is therefore a first-class path, and not a fallback that reports nothing.

A module zip is built with `golang.org/x/mod/zip` from an extracted tarball, and never by hand. That package is the code the go command validates against. A zip that is merely close is a checksum failure at every consumer. The tarball is spooled to disk, so peak memory is a buffer rather than the module's size.

## Cache

A resolved version lives in `goproxy_modules` and `goproxy_versions`. A zip is an ordinary content-addressed blob. A cached module is deliberately **not** a project and not a release. A cached upstream module must never surface in the browse frontend, in a package-manager surface, in `latest` resolution, or in a download URL.

The zip key is registered in `IsBlobReferenced`, so retention's GC does not sweep the cache out from under a healthy proxy.

Concurrent fetches of the same version are collapsed by a single-flight, so a cold cache under a parallel `go mod download` builds each zip once.

## Admin dashboard

The **Go Proxy** page shows health and the credential first, then the cache size. It then shows a per-module table. That table's `last_error_kind` field is what makes a credential failure visible. It then shows recent requests with their outcome.

The per-module last error is persisted. The counters and the recent-request ring live in memory, and the page labels them "since start". A write per request, for a number only a dashboard reads, is not worth its cost. The one thing that must survive a restart does survive it.

`POST /api/goproxy/recheck` re-runs the readiness probe on demand, so an operator who just fixed a credential does not wait out the poll interval.
