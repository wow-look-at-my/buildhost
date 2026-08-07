# GitHub Deployments integration (.github/actions)

Extracted verbatim from CLAUDE.md; paragraph breaks were added at the existing
topic boundaries, no wording changed.

The three TOP-LEVEL publish composites -- `buildhost-publish`,
`buildhost-publish-site`, `buildhost-publish-docker` -- register each publish as a
GitHub Deployment in the calling repo (input `deployment_environment`): a `Create
GitHub Deployment` step (`id: deployment`, right after the no-all-builds guard)
creates the deployment plus an `in_progress` status, and a `Finish GitHub
Deployment` step (`id: deployment-status`, `if: always() && deployment_id != ''`)
posts the terminal `success`/`failure` status whose `environment_url` is the live
buildhost URL -- the release page for a single-project `buildhost-publish`
(`{server}/projects/{proj}/releases/{rv}`, via the publish script's new
`deployment_url` output; a multi-project fan-out links the primary project's
page), the `site_url` for `buildhost-publish-site`, and
`{server}/projects/{project}` for `buildhost-publish-docker`.

Environment naming: `buildhost/{project}` (publish + docker share it deliberately
-- same logical project), `buildhost/{project}/{branch}` for sites (PR previews
land as e.g. `buildhost/myrepo/pr-12`, flagged `transient_environment`).

Two API parameters are load-bearing on every create: `required_contexts: []` (the
API default is "all commit statuses must be green", and the org's `all-builds`
status is still pending on that sha mid-run -> 409 Conflict) and `auto_merge:
false` (the default would try to merge the default branch into the ref).

## The deployment's `ref` decides whether anyone can find it

GitHub stores a deployment's `ref` verbatim. A deployment created against a bare
commit SHA therefore belongs to no branch, and every branch-scoped view -- the
branch and PR deployment panels -- reports "This branch has not been deployed /
No deployments", however healthy the deployment is. Creating it against a BRANCH
NAME populates both fields: GitHub resolves the branch and fills `sha` itself, so
the commit-level association is kept too. A branch name is strictly more
discoverable than a SHA, never less.

All three top-level composites take an optional `deployment_ref`, defaulting to
what they used before it existed, so nothing changes until a caller opts in:
`deployment_ref || git_commit || github.sha` for `buildhost-publish` and
`buildhost-publish-site`, `deployment_ref || github.sha` for
`buildhost-publish-docker` (which has no commit input). It is a separate input
because `git_commit` cannot serve double duty: that one is the recorded commit of
the release or site (the site sends it as `X-Git-Commit` and stores it as the
record's version) and has to stay a real SHA. Callers that want the publish to
surface on the branch pass a branch name:

```yaml
    deployment_ref: ${{ github.head_ref || github.ref_name }}
```

`github.head_ref` is the PR's source branch and is empty off `pull_request`, so
that expression is the branch in both cases. Do NOT use `github.ref_name` alone
on a `pull_request` event -- there it is the synthetic merge ref (`23/merge`).
`github.sha` carries the matching trap: it is the merge COMMIT, which is in no
branch and stops existing when the PR closes.

## Registering the deployment is part of publishing, and failure is RED

There is no `create_deployment` opt-out and no warn-and-continue: a failed create
(403 `Resource not accessible by integration` when the job lacks `deployments:
write`, 404, network), a 2xx carrying no deployment id, or a failed terminal
status all `setFailed`. The previous graceful-skip contract was an opt-out by
neglect -- an audit found SEVEN of the eight callers silently creating no
deployments at all, every one of them green, because nobody reads a warning in a
passing run. `deployments: write` is ADDITIVE to each caller's existing permission
set (job-level `permissions:` blocks replace workflow-level ones) and every caller
now declares it. The terminal-status step keeps `if: always() && deployment_id !=
''`: when the create genuinely failed the job is already failing, and there is no
deployment to finish.

The ONE skip is the same shape as the storage-record loopback rule and equally not
a switch: a deployment asserts "this publish is live at `<environment_url>`", so
against a loopback or plain-http server (buildhost's own
`upload-artifact-action-e2e` spawns one on `http://localhost:18080`) there is
nothing true to assert -- granting that job `deployments: write` instead would
mint a real GitHub Deployment per CI run pointing at an address nothing can reach.

The deployment steps authenticate via the typescript action's own `github-token`
input default (`${{ github.token }}`), never via the publish step's
`GITHUB_TOKEN` env (deliberately empty in `buildhost-publish`'s `path` mode). The
docker action gates the whole thing on `push == 'true'` (build-only runs deploy
nothing) and computes the terminal state from the build outcome plus the push-path
step outcomes (step ids `refs`, `build`, `setup-go`, `cli`, `push-buildhost`,
`push-foreign`; `skipped` is fine -- those steps are conditional on tag targets --
but the build itself must be `success`, which also catches a pre-build failure
leaving everything downstream skipped).

The LOW-LEVEL blocks (create-release, upload-artifact, publish-release, ...)
deliberately create NO deployments: one deployment per publish flow, and
buildhost's own `upload-artifact-action-e2e` CI job chains those blocks against
`http://localhost:18080` where a deployment would be meaningless. A server-side
path for CLI/non-GHA publishers (buildhost's GitHub App creating the deployment
from the verified OIDC repo identity, best-effort like the default-branch lookup)
is a deferred follow-up.
