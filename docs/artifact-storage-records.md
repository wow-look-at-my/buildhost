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

---

The rest of this document was extracted verbatim from CLAUDE.md; paragraph breaks
follow the original bold-lead structure and no wording was changed.

## No separate step, no separate action

The POST happens INSIDE each publish step's own script -- `buildhost-publish`'s
`publish` step (`postStorageRecords`, draining the records `uploadAndPublish`
accumulated), `buildhost-publish-site`'s `publish` step (`postStorageRecord`,
called from `finishPublish`, which is `async` for exactly this reason -- all three
call sites await it), and `buildhost-publish-docker`'s `Push to buildhost
(chunked)` step, right after the CLI push it describes. An earlier draft factored
this into a reusable `wow-look-at-my/actions@artifact-storage-record#latest`
invoked as its own step; that was rejected because a separate step is a thing a
pipeline can be assembled without. It also failed concretely: GitHub resolves a
composite's `uses:` refs EAGERLY, before evaluating any step's `if:`, so an
unpublished tag broke `upload-artifact-action-e2e` even though that job had
recording turned off -- proof the dependency was real while the recording was not.
The composites' step lists are therefore unchanged from before this feature.

## The low-level chain records too

`buildhost-publish-release` -- the final step of the create-release ->
upload-artifact -> publish-release chain that publishers assemble when they do not
use `buildhost-publish` (gosmopolitan, cc-marketplace, competent-search-thing) --
records every artifact the publish just made public. Without it that whole path
stored real, permanent, consumer-visible artifacts and recorded nothing; treating
those blocks as "components, not the pipeline" was wrong, because for those repos
the chain IS the pipeline. It needs the artifact list to do this and there is no
artifacts-listing endpoint, so **both `PublishRelease` and `GetRelease` return
`publishedRelease`** (the `db.Release` embedded, so every pre-existing top-level
field is unchanged for older clients, plus `artifacts`). Publish already loaded
them for its no-artifacts check, so that costs nothing. Pinned by
`TestPublishRelease_ReturnsPublishedArtifacts` and
`TestGetRelease_ReturnsArtifacts`.

A publish response carrying no `artifacts` FAILS outright -- there is deliberately
no retry. It once had one, because a publish served mid-rollout by the previous
container came back without the field and failed an unrelated repo's CI (observed
live: cc-marketplace published at ~21:49 mid-rollout and failed while two sibling
repos publishing minutes later succeeded). That was a deploy defect, not weather,
and it is fixed at the source in docker-updater (see `docs/deploy-and-updates.md`):
the replacement container no longer answers to the service alias before it is
healthy, so a publish is served by exactly one version. With that, an absent
`artifacts` means a server genuinely older than this action -- a version to
upgrade, which a retry would only convert into a slow failure. `buildhost-publish`
POSTs the publish endpoint directly rather than through this composite, so nothing
is recorded twice.

## No warn-and-continue anywhere in the record path

A publish whose response carries no artifacts (an older server) `setFailed`s
rather than warning -- a publish that records nothing must not report success --
and an `artifact_url` over the API's 152-char cap fails rather than quietly
dropping the URL, since a record without the link back to the bytes its digest
covers is a silent downgrade. The only skip left is the loopback/non-https
registry check, which is a property of the target rather than a switch.

## No opt-out input, and failure is RED

There is no `create_storage_record` input on any composite. The calling job needs
`artifact-metadata: write` (ADDITIVE -- a job-level `permissions:` block replaces
the workflow-level one), and without it the publish FAILS with a message naming
the exact permission. A warn-and-continue path was rejected as an opt-out by
neglect: the publish would stay green while the org's page silently fell behind
reality, which is precisely the deceptive green the requirements-are-CI-checks
rule forbids. This is a deliberate divergence from the deployment steps'
graceful-skip contract -- a deployment is a nicety, a record of what the registry
now holds is part of the publish being complete. The LOW-LEVEL blocks
(create-release, upload-artifact, publish-release, ...) post no records: they are
components, and the top-level composites are the pipeline.

**The one thing that suppresses a record is a property of the target, not a
switch:** a `registry_url` whose host is `localhost`/`127.0.0.1`/`::1` is skipped
with an info line, because a loopback server is not a registry anything can fetch
from and the row would be permanently unreachable. That is what keeps buildhost's
own `upload-artifact-action-e2e` (which publishes sites to
`http://localhost:18080`) from writing junk into the org's inventory; every real
publish records unconditionally.

## API field constraints that are easy to get wrong

From GitHub's OpenAPI description, and each one has bitten: `github_repository` is
the repo NAME only -- its pattern is `^[A-Za-z0-9.\-_]+$`, so an `owner/repo`
value is rejected outright and fails every publish (the org is already the
endpoint's path parameter). `registry_url` and `artifact_url` must match
`^https://`, which is a second reason the loopback skip exists -- a plain-http
server cannot be recorded at all. `artifact_url` is capped at **152** characters,
short enough for a deeply namespaced project to exceed, so an over-long URL is
dropped with a warning rather than failing an otherwise-successful publish.
`digest` must match `^sha256:[a-f0-9]{64}$` (lowercase). `name`, `repository` and
`path` carry no pattern, so slash-namespaced project names and `<os>/<arch>` paths
are fine.

## The recorded digest is always the sha256 of the bytes as UPLOADED

This is the load-bearing subtlety: buildhost strips and repackages on demand, so
for an ELF the default `raw` download is NOT byte-identical to the upload
(non-ELF artifacts -- APE, PE, Mach-O -- are served as-is, so they happen to
match). `debug=1` is the one download that returns the uploaded bytes verbatim,
which is why `buildhost-publish` points each record's `artifact_url` at
`.../{project}?v=&os=&arch=&debug=1`: a storage record whose URL serves bytes
hashing to something other than its digest is worse than no record, and the
uploaded digest is also the one a future `actions/attest` provenance subject would
cover. Consequently `buildhost-publish` now hashes every artifact unconditionally
(previously only when the server advertised `upload_by_sha256`) -- one extra
streaming read of a file the upload loop reads anyway. Per-composite shape:

- **`buildhost-publish`**: one record per os/arch slot per project,
  `name`/`repository` = the buildhost project (namespaced `<repo>/<binary>`
  included), `version` = the release version, `path` = `<os>/<arch>`. Records are
  accumulated by `uploadAndPublish` (hash-reference registrations included, since
  those are real artifact rows) and drained by `postStorageRecords` at both exits
  -- the namespaced path and the legacy flat fallback.
- **`buildhost-publish-docker`**: one record for the buildhost-bound image. The
  digest is read from the exported OCI layout's `index.json` root descriptor -- on
  this path buildx does not push (it writes a layout for the chunk-aware CLI), so
  that descriptor, not a buildx output, is the digest buildhost stores and serves;
  a layout with no root digest, or an unparseable reference, FAILS the push rather
  than skipping the record. `artifact_url` is the real manifest URL
  (`https://oci.{domain}/v2/{project}/manifests/{digest}`). Tags targeting foreign
  registries are deliberately not recorded -- another registry's inventory is its
  own to account for.
- **`buildhost-publish-site`**: records the uploaded tar.gz/zip archive's sha256
  and carries **no** `artifact_url` (a site is served unpacked, so no URL returns
  those bytes, and a record must never point at bytes that hash to something
  else). Because this runs on every PR-preview push, the reusable
  `buildhost-preview.yml` and buildhost's own `preview.yml` both declare
  `artifact-metadata: write`.

## Retention retracts what it evicts

An evicted release's artifacts are no longer fetchable at the URL their storage
record advertises, so `internal/retention` marks those records `status: deleted`
(`artifactmetadata.go`, `GitHubRecordDeleter`) -- otherwise the org's linked
artifacts page only grows, asserting buildhost holds bytes it deleted. Three
things make this work where CI cannot: eviction runs in the background sweeper or
the `gc` CLI with no workflow in the picture; the digests are captured BEFORE the
rows go (`collectRecords`), since after eviction nothing could reconstruct them;
and it authenticates as buildhost itself via `auth.BearerForRepo` (App
installation token, else the static PAT), addressing the org from the project's
recorded `github_repo`.

`registry_url` must equal what the publishing CI recorded, i.e. buildhost's own
public base URL -- the server is otherwise never told its own URL, so this one
path reads `BUILDHOST_PRIMARY_DOMAIN`. A project with no `github_repo` is skipped
(nothing addressable was ever posted); a *failure* never aborts the eviction (the
bytes are already gone, and refusing to GC over a GitHub outage is worse) but is
counted in `Report.RecordsMarkedDeleted`/`RecordsUnmarked`/`RecordErrors`, printed
by `gc` (which exits NON-ZERO on any unmarked record in enforce mode), and logged
at WARN by the sweeper. A dry run reports what it WOULD retract and calls nothing.
Prerequisite: the GitHub App needs artifact-metadata write on the org -- a 403 says
exactly that. `TestRun_NoDeleterReportsUnmarkedRatherThanSilentlySkipping` is the
load-bearing test: eviction with no deleter wired must REPORT the gap, never pass
quietly.
