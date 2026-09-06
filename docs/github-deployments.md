# GitHub Deployments integration (.github/actions)

Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed.

The three TOP-LEVEL publish composites are `buildhost-publish`, `buildhost-publish-site` and `buildhost-publish-docker`. Each registers its publish as a GitHub Deployment in the calling repo, under the input `deployment_environment`. A `Create GitHub Deployment` step (`id: deployment`, right after the no-all-builds guard) creates the deployment plus an `in_progress` status. A `Finish GitHub Deployment` step (`id: deployment-status`, `if: always() && deployment_id != ''`) posts the terminal `success` or `failure` status.

That status carries an `environment_url`, the live buildhost URL. For a single-project `buildhost-publish` it is the release page, `{server}/projects/{proj}/releases/{rv}`, from the publish script's `deployment_url` output. A multi-project fan-out links the primary project's page. For `buildhost-publish-site` it is the `site_url`. For `buildhost-publish-docker` it is `{server}/projects/{project}`.

Environment naming is `buildhost/{project}`. publish and docker share it deliberately, because it is the same logical project. A site is `buildhost/{project}/{branch}`, so a PR preview lands as `buildhost/myrepo/pr-12` and is flagged `transient_environment`.

Two API parameters are load-bearing on every create. `required_contexts: []` is one. The API default is "all commit statuses must be green", and the org's `all-builds` status is still pending on that sha mid-run, which answers 409 Conflict. `auto_merge: false` is the other. The default tries to merge the default branch into the ref.

## The deployment's `ref` decides whether anyone can find it

GitHub stores a deployment's `ref` verbatim. A deployment created against a bare commit SHA therefore belongs to no branch. Every branch-scoped view, the branch panel and the PR deployment panel, then reports "This branch has not been deployed" or "No deployments", however healthy the deployment is. A create against a BRANCH NAME populates both fields. GitHub resolves the branch and fills `sha` itself, so the commit-level association is kept too. A branch name is strictly more discoverable than a SHA, never less.

All three top-level composites take an optional `deployment_ref`. Each defaults to what it used before that input existed, so nothing changes until a caller opts in. `buildhost-publish` and `buildhost-publish-site` use `deployment_ref || git_commit || github.sha`. `buildhost-publish-docker` uses `deployment_ref || github.sha`, because it has no commit input. It is a separate input, because `git_commit` cannot serve double duty. That one is the recorded commit of the release or the site. The site sends it as `X-Git-Commit` and stores it as the record's version. It has to stay a real SHA. A caller that wants the publish to surface on the branch passes a branch name:

```yaml
    deployment_ref: ${{ github.head_ref || github.ref_name }}
```

`github.head_ref` is the PR's source branch. It is empty off `pull_request`. That expression is therefore the branch in both cases. Do NOT use `github.ref_name` alone on a `pull_request` event. There it is the synthetic merge ref (`23/merge`). `github.sha` carries the matching trap. It is the merge COMMIT. That commit is in no branch, and it stops existing when the PR closes.

## Registering the deployment is part of publishing, and failure is RED

There is no `create_deployment` opt-out and no warn-and-continue. Three things all `setFailed`. A failed create is one: a 403 `Resource not accessible by integration` when the job lacks `deployments: write`, a 404, or a network error. A 2xx that carries no deployment id is another. A failed terminal status is the third. The previous graceful-skip contract was an opt-out by neglect. An audit found SEVEN of the eight callers silently creating no deployments at all. Every one of them was green. Nobody reads a warning in a passing run. `deployments: write` is ADDITIVE to each caller's existing permission set, because a job-level `permissions:` block replaces the workflow-level one. Every caller now declares it. The terminal-status step keeps `if: always() && deployment_id != ''`. When the create genuinely failed the job is already failing, and there is no deployment to finish.

The ONE skip has the same shape as the storage-record loopback rule. It is equally not a switch. A deployment asserts "this publish is live at `<environment_url>`". Against a loopback or plain-http server there is nothing true to assert. buildhost's own `upload-artifact-action-e2e` spawns one on `http://localhost:18080`. Granting that job `deployments: write` instead mints a real GitHub Deployment per CI run, pointed at an address nothing can reach.

The deployment steps authenticate through the typescript action's own `github-token` input default, `${{ github.token }}`. They never use the publish step's `GITHUB_TOKEN` env, which is deliberately empty in `buildhost-publish`'s `path` mode. The docker action gates the whole thing on `push == 'true'`, because a build-only run deploys nothing. It computes the terminal state from the build outcome plus the push-path step outcomes. Those step ids are `refs`, `build`, `setup-go`, `cli`, `push-buildhost` and `push-foreign`. A `skipped` there is fine, because those steps are conditional on tag targets. The build itself must be `success`, which also catches a pre-build failure that leaves everything downstream skipped.

The LOW-LEVEL blocks (create-release, upload-artifact, publish-release, and the rest) deliberately create NO deployments. There is one deployment per publish flow. buildhost's own `upload-artifact-action-e2e` CI job also chains those blocks against `http://localhost:18080`, where a deployment is meaningless. A server-side path for a CLI or non-GHA publisher is a deferred follow-up. There, buildhost's GitHub App creates the deployment from the verified OIDC repo identity, best-effort like the default-branch lookup.
