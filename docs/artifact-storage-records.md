# Artifact storage records: three kinds, one module

Every publish composite posts storage records to GitHub's artifact metadata API
(`POST /orgs/{org}/artifacts/metadata/storage-record`) so what buildhost stores
shows up on the org's linked artifacts page.

buildhost stores exactly **three kinds of thing**, so
`.github/actions/lib/storage-record.js` exports three functions and each
composite calls the one matching what it just published:

| Kind | Function | Called by |
|---|---|---|
| release artifacts | `recordReleaseArtifacts` | `buildhost-publish`, `buildhost-publish-release` |
| a static site archive | `recordSite` | `buildhost-publish-site` |
| a pushed image | `recordImage` | `buildhost-publish-docker` |

Four callsites, three kinds: `buildhost-publish` and `buildhost-publish-release`
record the same kind, because `buildhost-publish` POSTs the publish endpoint
directly rather than going through the composite. They used to DERIVE that kind
independently -- two copies of the `dl.<host>` origin, the `debug=1` URL and the
`sha256:` digest, which is exactly how the artifact_url/digest agreement drifts.
Now both call one function.

**Callers pass what they published, never a record.** Project, version, slots,
digests; every field, URL, label and message is derived in the module. A
composite's whole footprint is an import and a call:

```ts
import { recordSite } from "${{ github.action_path }}/../lib/storage-record";
await recordSite(octokit, core, context, { server, project, branch, version: gitCommit, sha256: sum });
```

`buildhost-publish` adds two lines because it accumulates slots during upload
and posts once at the end -- it still restates no part of the record shape.

## Why a module and not a step

The recording must happen INSIDE each publish step. A separate step is a thing a
pipeline can be assembled without, and an earlier draft that factored this into
a reusable `wow-look-at-my/actions@artifact-storage-record#latest` failed
concretely: GitHub resolves a composite's `uses:` refs EAGERLY, before any
step's `if:` is evaluated, so an unpublished tag broke a job that had recording
turned off. Sharing SOURCE keeps that invariant -- the code is inlined into the
same step it always ran in.

## How the import resolves

- `github.action_path` is the action's own directory. For a remote composite
  GitHub downloads the WHOLE repo, so sibling directories are on disk during a
  caller's run -- the same mechanism `buildhost-publish-docker` already uses to
  build the CLI from `${{ github.action_path }}/../../..`.
- The typescript action lifts top-level `import`s to real module scope
  (`transform.ts`), so the statement is legal even though the rest of the script
  becomes an async function body.
- The specifier carries **no extension** deliberately: tsc (moduleResolution
  Node10) resolves `storage-record.d.ts` for types, and node resolves
  `storage-record.js` at runtime. A `.ts` specifier would be rejected by tsc
  without `allowImportingTsExtensions`, and a bare `.ts` file would not resolve
  at runtime.
- The pair is hand-written, not generated: no build step, and nothing needs Node
  installed to use the actions. This mirrors the checked-in-JS precedent in
  `internal/admin/`.

## What the module decides

Each function returns `false` only when it has already called `core.setFailed`,
so a caller's `if (!ok) return` needs no message of its own. Recording has no
opt-out input and a failure is RED. Two things suppress a record, and both are
properties of the TARGET rather than a switch:

- **An unreachable registry** -- a `registry_url` on `localhost`/`127.0.0.1`/
  `::1`, or any non-`https://` URL. The row would point at bytes nothing can
  fetch, and the API only accepts https. This is what keeps buildhost's own
  `upload-artifact-action-e2e` (publishing to `http://localhost:18080`) out of
  the org's inventory.
- **A user-account owner** -- the endpoint is org-scoped, and a personal account
  has no linked artifacts page, so the POST 404s no matter what the token
  grants. The owner type is probed (`GET /users/{owner}`) ONLY after a 404, so
  organizations never pay the extra call, and it **fails closed**: an unreadable
  owner type leaves the original failure standing, so a genuine permissions 404
  on an org still fails the publish.

## Why a 404 message must not blame the permission

The first version of the user-account fix told the reader to add
`artifact-metadata: write` on any 403 or 404. That message cost real debugging
time on `PazerOP/UE553`: the grant was present (the runner's token dump listed
`ArtifactMetadata: write`, and the preceding GitHub Deployment step -- which
needs `deployments: write` from the same block -- succeeded), and the actual
cause was that `PazerOP` is a user, not an org.

So the two statuses now say different things, and the 404 reports the evidence
in hand rather than a guess:

- **403** -- a plain refusal for artifact metadata; name the grant.
- **404** -- the endpoint found no organization of that name this token can see,
  plus what `GET /users/{owner}` answered: `reported 'Organization'` (so the
  grant is the remaining suspect) or `failed, so the owner type is unknown` (the
  fail-closed case -- it may well be a user account that could not be
  confirmed).

## Tests

`.github/scripts/storage-record-test.ts`, run by the `storage-record` job in
`ci.yml`. It has to exist as its own job: the composites' own e2e publishes to a
loopback server, where the unreachable-registry skip returns before a single
record is posted, so every branch here would otherwise ship untested -- and a
mistake in this module fails publishing for every repo in the org at once.

Per kind: a release artifact's full derived record (asserted field by field,
including the `dl.` origin and `debug=1` URL), a namespaced fan-out where
entries carry their own project/version, a missing sha256 failing the publish, a
site's deliberate absence of `artifact_url`, an image's registry/repository/tag
split, and an unparseable reference recording nothing.

Shared: the empty-list no-op, each unreachable-registry form, the user-account
skip, an organization's 404 failing closed with the probe's answer quoted, an
unreadable owner type failing closed, a 403 naming the grant without costing a
lookup, a successful POST costing no lookup, the 152-char `artifact_url` cap
failing rather than dropping the link, and a non-HTTP failure keeping its bare
message.
