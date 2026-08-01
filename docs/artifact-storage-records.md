# Artifact storage records

Every publish composite records what buildhost stored on the org's linked
artifacts page (`POST /orgs/{org}/artifacts/metadata/storage-record`).

## Three kinds, four callsites

buildhost stores three kinds of thing, so `.github/actions/lib/storage-record.ts`
exports three functions: `recordReleaseArtifacts` (buildhost-publish,
buildhost-publish-release), `recordSite` (buildhost-publish-site), `recordImage`
(buildhost-publish-docker).

Two composites record the same kind because `buildhost-publish` is the whole
pipeline in one step while `buildhost-publish-release` is the last step of the
`create-release -> upload-artifact -> publish-release` chain that publishers
assemble themselves. The pipeline cannot be built from those components -- a
composite action has no loop, so it cannot invoke `upload-artifact` once per
file -- so it reimplements the chain inline. That is why the record derivation
was a second copy, with its own `dl.<host>` origin, `debug=1` URL and `sha256:`
digest.

**Callers pass what they published, never a record.** Every field, URL, label
and message is derived in the module, so the whole per-composite footprint is:

```ts
const { recordSite } = require("${{ github.action_path }}/../lib/storage-record.ts") as typeof import("${{ github.action_path }}/../lib/storage-record");
await recordSite(octokit, core, context, { server, project, branch, version: gitCommit, sha256: sum });
```

`require` takes the explicit `.ts` (node strips the types); the `typeof import`
cast is extensionless so tsc resolves the same source for types -- no build step
and no hand-written `.d.ts` to drift. `github.action_path` reaches the module
because GitHub downloads the whole repo for a remote composite, the same way
`buildhost-publish-docker` builds the CLI from `${{ github.action_path }}/../../..`.

## Why source and not a step

An earlier draft made this a reusable action invoked as its own step. Rejected:
a separate step is a thing a pipeline can be assembled without, and it failed
concretely, since GitHub resolves a composite's `uses:` refs eagerly, before any
step's `if:`, so an unpublished tag broke a job that had recording turned off.
Sharing source keeps the POST inlined in the step it always ran in.

## What suppresses a record

Recording has no opt-out and a failure is RED. Two things skip it, both
properties of the TARGET rather than a switch:

- **An unreachable registry** -- loopback or non-`https://`. The row would point
  at bytes nothing can fetch, and the API only accepts https. This keeps
  buildhost's own `upload-artifact-action-e2e` out of the org's inventory.
- **A user-account owner** -- the endpoint is org-scoped and a personal account
  has no linked artifacts page, so the POST 404s whatever the token grants. The
  owner type is probed (`GET /users/{owner}`) only after a 404, so organizations
  pay no extra call, and it fails closed: an unreadable owner type leaves the
  failure standing, so a genuine permissions 404 on an org still fails.

## Why a 404 must not blame the permission

The first version of that fix told the reader to add `artifact-metadata: write`
on any 403 or 404, which cost real debugging time on `PazerOP/UE553`: the grant
was present (the runner's token dump listed it, and the preceding Deployment
step needing `deployments: write` from the same block succeeded) and the cause
was that `PazerOP` is a user. So 403 names the grant, and 404 reports what the
probe observed -- `reported 'Organization'` (the grant is the remaining suspect)
or `failed, so the owner type is unknown`.

## Tests

`.github/scripts/storage-record-test.ts`, run by the `storage-record` CI job. It
needs its own job because the composites' own e2e publishes to a loopback
server, where the skip returns before a record is posted -- so every branch here
would otherwise ship untested, and a mistake fails publishing org-wide.
