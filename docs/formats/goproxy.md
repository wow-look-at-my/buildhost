# Go module proxy

`internal/goproxy/`. Serves the [Go module download protocol][protocol] on
`goproxy.{domain}`, replacing a separately-deployed [Athens][athens].

[protocol]: https://go.dev/ref/mod#goproxy-protocol
[athens]: https://github.com/gomods/athens

## What replaced what, and why

The Athens deployment answered `404` with an empty body for every private
first-party module while serving public ones from the same org perfectly. The
upstream cause was visible only on `@v/list`, which alone returned a body:

```
not found: module github.com/wow-look-at-my/tml: git ls-remote -q
https://github.com/wow-look-at-my/tml in /tmp/athens399040138/...: exit status 128:
        fatal: could not read Username for 'https://github.com': terminal prompts disabled
```

That is not an unapproved token. It is **no credential at all**: Athens'
`ATHENS_GITHUB_TOKEN` was unset, so the `git` subprocess it shells out to had
nothing to present, prompted for a username, and died. Two defects compounded:

1. **A subprocess turns a credential failure into an opaque exit code.** The
   real error was four layers down inside a child process's stderr.
2. **The failure was reported as `404`.** At the protocol level that means "this
   module does not exist", so `go mod download` reports a missing module and the
   reader goes looking for a typo in `go.mod` instead of at the credential.

Both are design properties here, not bug fixes:

- **No subprocess.** Content comes from the GitHub REST API and the public
  mirror over `net/http`. Every failure is an HTTP status, classified in
  `errors.go`.
- **Only a genuine absence may be a `404`.** `Kind` decides the status, in one
  place, and `asError` classifies an unrecognized error as `KindUpstream` rather
  than guessing absence.

## Status mapping

| Kind | Status | Means |
| --- | --- | --- |
| `not_found` | 404 | Upstream was readable and the module is genuinely absent. |
| `unauthorized` | 403 | The proxy's own credential was rejected, or it has none. |
| `upstream` | 502 | Upstream failed, was unreachable, or rate-limited. |
| `invalid_request` | 400 | Not a well-formed module-proxy request. |

The response body is the whole diagnosis: `go mod download` prints a proxy's
body verbatim, so it names the module, the version, the upstream and its status,
and for an authorization failure it says outright that this is not a missing
module.

Two ambiguities are resolved deliberately:

- **GitHub `404` with no credential is `unauthorized`, not `not_found`.** GitHub
  will not confirm a private repository exists to someone unauthorized, so with
  no credential the two are strictly indistinguishable -- and reporting absence
  is the exact laundering above. With a credential present, a `404` is reported
  as `not_found`, and the detail says the credential was presented so a reader
  can tell an access problem from a real absence.
- **A rate-limit `403` is `upstream`, not `unauthorized`.** It clears on its own;
  reporting it as an authorization failure sends the reader after a credential
  that was never at fault. Detected from `X-RateLimit-Remaining: 0`,
  `Retry-After`, or GitHub's own message.

## Readiness

A module proxy with no credential serves every public module and none of the
private ones. Nothing that only asks "is the process up" can see that, which is
why it went unnoticed. So readiness is its own statement, re-checked every 15
minutes, reported on the admin dashboard and at `goproxy.{domain}/health` (503
when not ready), and logged at ERROR at startup.

It reports three distinct states:

- **Not ready** -- no credential while private prefixes are configured, or the
  configured readiness module did not resolve.
- **Ready but unproven** -- a credential exists but no
  `BUILDHOST_GOPROXY_READINESS_MODULE` is set. A credential that authenticates
  but is not authorized for the org looks identical to a working one from here,
  so the check says so rather than claiming a proof it cannot make. **Set a
  private module here.** It is the only configuration that catches the failure
  class this package exists for.
- **Ready** -- the readiness module resolved.

It deliberately does NOT fail the registry's `/healthz`. A goproxy
misconfiguration would then take every other buildhost service out of rotation,
which is a worse outcome than the one being prevented.

## Configuration

| Env var | Default | Meaning |
| --- | --- | --- |
| `BUILDHOST_GOPROXY_PRIVATE_PREFIXES` | `github.com/<org>` per `BUILDHOST_OIDC_ORGS` | Module prefixes fetched direct from GitHub with buildhost's credential. |
| `BUILDHOST_GOPROXY_UPSTREAM` | (unset) | Optional mirror to forward non-private modules to. Off by default. |
| `BUILDHOST_GOPROXY_READINESS_MODULE` | (unset) | A PRIVATE module resolved at startup to prove the credential works. |

The credential is buildhost's existing one -- `BUILDHOST_GITHUB_APP_ID` +
`BUILDHOST_GITHUB_APP_PRIVATE_KEY`, else `BUILDHOST_GITHUB_TOKEN` (see
`docs/running.md`). There is no goproxy-specific credential: a second one is a
second thing to leave unset. A GitHub App installed on the org with
`contents: read` is the right answer, and avoids the fine-grained-PAT
org-approval trap entirely.

Private prefixes default to the configured OIDC orgs, so a deployment that
already declares which orgs it serves needs no extra configuration.

## No third-party mirror by default

`BUILDHOST_GOPROXY_UPSTREAM` is unset out of the box, and buildhost never picks a
mirror for you. A module mirror sees the path of every dependency routed through
it, so defaulting to one would hand a third party the org's entire dependency
graph -- including the path of any private module whose prefix was not listed in
`BUILDHOST_GOPROXY_PRIVATE_PREFIXES`, which is the case where a leak is worst and
least visible. Athens, which this replaces, ran `GOPROXY=direct` for the same
reason; defaulting to a mirror here would have been a regression, not a port.

A module this proxy does not serve is answered **404**, which in a `GOPROXY` list
is the protocol's "try the next entry":

```
GOPROXY=https://goproxy.pazer.build,direct
```

Everything outside the org is then fetched straight from its origin, exactly as
`GOPROXY=direct` did. Measured, not assumed: `go` advances to the next entry on
404 and 410, and halts on any other status (403, 502, ...). That is why an
authorization failure stays a 403 -- a credential problem must halt and be
reported, never fall through to `direct` and quietly succeed while the proxy is
misconfigured.

An operator who does want a mirror sets `BUILDHOST_GOPROXY_UPSTREAM` explicitly,
and can point it at a self-hosted one.

## Auth

Every request needs a read-scoped token (or a GitHub browser session), public
modules included. Gating only the private prefixes would make "is this module
private?" an oracle anyone could query anonymously.

Point the toolchain at it with `~/.netrc`:

```
machine goproxy.pazer.build login x password <read-scoped token>
```

## Module resolution

Module paths map onto GitHub repositories in `modpath.go`:

- `github.com/o/r` -> repo root, tags `vX.Y.Z`.
- `github.com/o/r/sub` -> the `sub` directory, tags `sub/vX.Y.Z`. This is a real
  shape in the org (`agentic-loop/go`), and getting it wrong makes the module
  unresolvable.
- `github.com/o/r/v2` -> either the repo root (whose `go.mod` declares the `/v2`
  path) or a `v2` subdirectory. Both are candidates, and the `go.mod` that
  actually declares the requested path settles it.

`@latest` resolves to the highest release version, else the highest pre-release,
else a **pseudo-version of the default branch head**. That last case is the
normal one for this org's untagged, branch-pinned first-party modules, so it is
a first-class path rather than a fallback that reports nothing.

Module zips are built with `golang.org/x/mod/zip` from an extracted tarball,
never hand-rolled: it is the code the go command validates against, and a zip
that is merely close is a checksum failure at every consumer. The tarball is
spooled to disk, so peak memory is a buffer rather than the module's size.

## Cache

Resolved versions live in `goproxy_modules` / `goproxy_versions`; zips are
ordinary content-addressed blobs. Cached modules are deliberately **not**
projects or releases -- a cached upstream module must never surface in the browse
frontend, the package-manager surfaces, `latest` resolution or a download URL.

The zip key is registered in `IsBlobReferenced`, so retention's GC does not sweep
the cache out from under a healthy proxy.

Concurrent fetches of the same version are collapsed by a single-flight, so a
cold cache under a parallel `go mod download` builds each zip once.

## Admin dashboard

The **Go Proxy** page shows health and the credential first, then cache size,
then a per-module table whose `last_error_kind` is the field that makes a
credential failure visible, then recent requests with their outcome. The
per-module last error is persisted; the counters and the recent-request ring are
in memory and labelled "since start" -- a write per request for numbers only a
dashboard reads is not worth it, and the one thing that must survive a restart
does.

`POST /api/goproxy/recheck` re-runs the readiness probe on demand, so an operator
who just fixed a credential does not wait out the poll interval.
