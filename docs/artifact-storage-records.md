# Artifact storage records

Every publish composite records what buildhost stored on the org's linked artifacts page (`POST /orgs/{org}/artifacts/metadata/storage-record`).

## Three kinds, four callsites

buildhost stores several kinds of thing. `.github/actions/lib/storage-record.ts` therefore exports a function for each one. `recordReleaseArtifacts` serves buildhost-publish and buildhost-publish-release. `recordSite` serves buildhost-publish-site. `recordImage` serves buildhost-publish-docker.

Two composites record the same kind, for one reason. `buildhost-publish` is the whole pipeline in one step. `buildhost-publish-release` is the last step of the `create-release -> upload-artifact -> publish-release` chain that a publisher assembles itself.

The pipeline cannot be built from those components. A composite action has no loop. It therefore cannot invoke `upload-artifact` once per file. It therefore reimplements the chain inline. That is why the record derivation was a second copy, with its own `dl.<host>` origin, `debug=1` URL and `sha256:` digest.

**A caller passes what it published, never a record.** The module derives every field, URL, label and message. The whole per-composite footprint is therefore this:

```ts
const { recordSite } = require("${{ github.action_path }}/../lib/storage-record.ts") as typeof import("${{ github.action_path }}/../lib/storage-record");
await recordSite(octokit, core, context, { server, project, branch, version: gitCommit, sha256: sum });
```

`require` takes the explicit `.ts`, because node strips the types. The `typeof import` cast is extensionless, so tsc resolves the same source for the types. There is therefore no build step, and no hand-written `.d.ts` to drift. `github.action_path` reaches the module because GitHub downloads the whole repo for a remote composite. `buildhost-publish-docker` builds the CLI from `${{ github.action_path }}/../../..` the same way.

## Why source and not a step

An earlier draft made this a reusable action invoked as its own step. That draft was rejected. A separate step is a thing a pipeline can be assembled without. It also failed concretely. GitHub resolves a composite's `uses:` refs eagerly, before any step's `if:`. An unpublished tag therefore broke a job that had recording turned off. Shared source keeps the POST inlined in the step it always ran in.

## What suppresses a record

Recording has no opt-out. A failure is RED. Two things skip it. Both are properties of the TARGET rather than a switch.

- **An unreachable registry** -- a loopback host, or a scheme other than `https://`. The row points at bytes nothing can fetch, and the API accepts only https. This keeps buildhost's own `upload-artifact-action-e2e` out of the org's inventory.
- **A user-account owner** -- the endpoint is org-scoped. A personal account has no linked artifacts page. The POST therefore answers 404 whatever the token grants. The owner type is probed with `GET /users/{owner}`, and only after a 404. An organization therefore pays no extra call. The probe fails closed. An unreadable owner type leaves the failure standing, so a genuine permissions 404 on an org still fails.

## Why a 404 must not blame the permission

The first version of that fix told the reader to add `artifact-metadata: write` on any 403 or 404. That cost real debugging time on `PazerOP/UE553`. The grant was present there. The runner's token dump listed it. The preceding Deployment step, which needs `deployments: write` from the same block, succeeded. The real cause was that `PazerOP` is a user.

A 403 therefore names the grant. A 404 reports what the probe observed. It says `reported 'Organization'`, where the grant is the remaining suspect. It otherwise says `failed, so the owner type is unknown`.

## Tests

`test/actions/storage-record.test.ts` holds them. `test/dats/action-libs.dats` runs it, in the `storage-record` CI job. It needs its own job. The composites' own e2e publishes to a loopback server, where the skip returns before a record is posted. Every branch here therefore ships untested without that job, and a mistake fails publishing org-wide.

---

The rest of this document was extracted verbatim from CLAUDE.md. Paragraph breaks follow the original bold-lead structure. No wording was changed.

## No separate step, no separate action

The POST happens INSIDE each publish step's own script. In `buildhost-publish` that is the `publish` step, where `postStorageRecords` drains the records `uploadAndPublish` accumulated. In `buildhost-publish-site` it is the `publish` step, where `finishPublish` calls `postStorageRecord`. `finishPublish` is `async` for exactly that reason, and every call site awaits it. In `buildhost-publish-docker` it is the `Push to buildhost (chunked)` step, right after the CLI push it describes.

An earlier draft factored this into a reusable `wow-look-at-my/actions@artifact-storage-record#latest`, invoked as its own step. That draft was rejected, because a separate step is a thing a pipeline can be assembled without. It also failed concretely. GitHub resolves a composite's `uses:` refs EAGERLY, before it evaluates any step's `if:`. An unpublished tag therefore broke `upload-artifact-action-e2e`, while that job had recording turned off. That proved the dependency was real while the recording was not. The composites' step lists are therefore unchanged from before this feature.

## The low-level chain records too

`buildhost-publish-release` is the final step of the create-release, upload-artifact, publish-release chain. A publisher that does not use `buildhost-publish` assembles that chain, as gosmopolitan, cc-marketplace and competent-search-thing do. That step records every artifact the publish just made public.

Without it, that whole path stored real, permanent, consumer-visible artifacts and recorded nothing. To treat those blocks as "components, not the pipeline" was wrong, because for those repos the chain IS the pipeline.

It needs the artifact list to do this, and there is no artifacts-listing endpoint. **Both `PublishRelease` and `GetRelease` therefore return `publishedRelease`.** That type embeds the `db.Release`, so every pre-existing top-level field is unchanged for an older client, and it adds `artifacts`. Publish already loaded them for its no-artifacts check, so that costs nothing. `TestPublishRelease_ReturnsPublishedArtifacts` and `TestGetRelease_ReturnsArtifacts` pin it.

A publish response that carries no `artifacts` FAILS outright. There is deliberately no retry. It once had one. A publish served mid-rollout by the previous container came back without the field, and failed an unrelated repo's CI. That was observed live: cc-marketplace published mid-rollout and failed, while two sibling repos published minutes later and succeeded.

That was a deploy defect, not weather. It is fixed at the source, in docker-updater. See `docs/deploy-and-updates.md`. The replacement container no longer answers to the service alias before it is healthy, so exactly one version serves a publish.

With that fix, an absent `artifacts` means a server genuinely older than this action. That is a version to upgrade. A retry only converts it into a slow failure. `buildhost-publish` POSTs the publish endpoint directly, rather than through this composite, so nothing is recorded twice.

## No warn-and-continue anywhere in the record path

A publish whose response carries no artifacts comes from an older server. The action calls `setFailed` there, rather than a warning. A publish that records nothing must not report success. An `artifact_url` over the API's character cap also fails, rather than a quiet drop of the URL. A record without the link back to the bytes its digest covers is a silent downgrade. The only skip left is the loopback and non-https registry check, which is a property of the target rather than a switch.

## No opt-out input, and failure is RED

There is no `create_storage_record` input on any composite. The calling job needs `artifact-metadata: write`. That grant is ADDITIVE, and a job-level `permissions:` block replaces the workflow-level one. Without the grant the publish FAILS, with a message that names the exact permission.

A warn-and-continue path was rejected as an opt-out by neglect. The publish then stays green while the org's page silently falls behind reality. That is precisely the deceptive green the requirements-are-CI-checks rule forbids.

This is a deliberate divergence from the deployment steps' graceful-skip contract. A deployment is a nicety. A record of what the registry now holds is part of the publish being complete. The LOW-LEVEL blocks post no record. create-release, upload-artifact and publish-release are among them. They are components, and the top-level composites are the pipeline.

**The one thing that suppresses a record is a property of the target, not a switch.** A `registry_url` whose host is `localhost`, `127.0.0.1` or `::1` is skipped, with an info line. A loopback server is not a registry anything can fetch from. The row is permanently unreachable. That is what keeps buildhost's own `upload-artifact-action-e2e` from writing junk into the org's inventory, because that job publishes a site to `http://localhost:18080`. Every real publish records unconditionally.

## API field constraints that are easy to get wrong

These come from GitHub's OpenAPI description, and each one has bitten.

`github_repository` is the repo NAME only. Its pattern is `^[A-Za-z0-9.\-_]+$`. An `owner/repo` value is therefore rejected outright, and it fails every publish. The org is already the endpoint's path parameter.

`registry_url` and `artifact_url` must match `^https://`. That is a second reason the loopback skip exists. A plain-http server cannot be recorded at all.

`artifact_url` is capped at **152** characters. A deeply namespaced project exceeds that. An over-long URL is therefore dropped with a warning, rather than a failure of an otherwise-successful publish.

`digest` must match `^sha256:[a-f0-9]{64}$`, in lowercase. `name`, `repository` and `path` carry no pattern, so a slash-namespaced project name and an `<os>/<arch>` path are both fine.

## The recorded digest is always the sha256 of the bytes as UPLOADED

This is the load-bearing subtlety. buildhost strips and repackages on demand. For an ELF the default `raw` download is therefore NOT byte-identical to the upload. A non-ELF artifact, such as an APE, a PE or a Mach-O, is served as-is, so it happens to match.

`debug=1` is the one download that returns the uploaded bytes verbatim. `buildhost-publish` therefore points each record's `artifact_url` at `.../{project}?v=&os=&arch=&debug=1`. A storage record whose URL serves bytes that hash to something other than its digest is worse than no record. The uploaded digest is also the one a future `actions/attest` provenance subject covers.

`buildhost-publish` therefore hashes every artifact unconditionally. It previously did that only when the server advertised `upload_by_sha256`. The cost is one extra streaming read of a file the upload loop reads anyway.

The per-composite shape follows.

- **`buildhost-publish`**: one record per os/arch slot per project. `name` and `repository` are the buildhost project, and that includes a namespaced `<repo>/<binary>`. `version` is the release version. `path` is `<os>/<arch>`. `uploadAndPublish` accumulates the records, and a hash-reference registration is included, because those are real artifact rows. `postStorageRecords` drains them at both exits, which are the namespaced path and the legacy flat fallback.
- **`buildhost-publish-docker`**: one record for the buildhost-bound image. The digest is read from the exported OCI layout's `index.json` root descriptor. buildx does not push on this path. It writes a layout for the chunk-aware CLI instead. That descriptor, and not a buildx output, is therefore the digest buildhost stores and serves. A layout with no root digest FAILS the push, and so does an unparseable reference. Neither skips the record. `artifact_url` is the real manifest URL, `https://oci.{domain}/v2/{project}/manifests/{digest}`. A tag that targets a foreign registry is deliberately not recorded, because another registry's inventory is its own to account for.
- **`buildhost-publish-site`**: it records the uploaded tar.gz or zip archive's sha256. It carries **no** `artifact_url`. A site is served unpacked, so no URL returns those bytes, and a record must never point at bytes that hash to something else. This runs on every PR-preview push, so every job that publishes a site declares `artifact-metadata: write`.

## Retention retracts what it evicts

An evicted release's artifacts are no longer fetchable at the URL their storage record advertises. `internal/retention` therefore marks those records `status: deleted`, in `artifactmetadata.go` through `GitHubRecordDeleter`. Otherwise the org's linked artifacts page only grows, and asserts that buildhost holds bytes it deleted.

Several properties make this work where CI cannot. Eviction runs in the background sweeper or in the `gc` CLI, with no workflow in the picture. `collectRecords` captures the digests BEFORE the rows go, because after eviction nothing can reconstruct them. The deleter authenticates as buildhost itself, through `auth.BearerForRepo`, with the App installation token or else the static PAT. It addresses the org from the project's recorded `github_repo`.

`registry_url` must equal what the publishing CI recorded, which is buildhost's own public base URL. The server is otherwise never told its own URL. This one path therefore reads `BUILDHOST_PRIMARY_DOMAIN`.

A project with no `github_repo` is skipped, because nothing addressable was ever posted. A *failure* never aborts the eviction. The bytes are already gone, and a refusal to GC over a GitHub outage is worse. Such a failure is counted in `Report.RecordsMarkedDeleted`, `Report.RecordsUnmarked` and `Report.RecordErrors`. `gc` prints those counts, and it exits NON-ZERO on any unmarked record in enforce mode. The sweeper logs them at WARN.

A dry run reports what it retracts and calls nothing. The GitHub App needs artifact-metadata write on the org. A 403 says exactly that. `TestRun_NoDeleterReportsUnmarkedRatherThanSilentlySkipping` is the load-bearing test. An eviction with no deleter wired must REPORT the gap, and must never pass quietly.
