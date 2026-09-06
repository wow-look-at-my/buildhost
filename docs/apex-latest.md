# Apex `latest` and the per-project default branch

Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed.

Apex `latest` (a download with no `?v=` and no `?branch=` -- e.g. `dl/{project}/latest/{os}/{arch}`) resolves to the newest published release on the project's **default branch** -- a per-project field (`projects.default_branch`, `migrations/011_project_default_branch.sql`) that defaults to `master` (`db.LatestBranch`).

buildhost learns the real default branch **itself, and the publish sends nothing**. On a GitHub-OIDC write the centralized `requireProject` reads `owner/repo` from the verified OIDC subject (`repo:OWNER/REPO:...`). It then asks GitHub for that repo's default branch (`auth.GitHubDefaultBranch`, a `GET api.github.com/repos/{owner}/{repo}`). The call is best-effort, cached, and gated on the GitHub Actions issuer. It records the answer through `db.SetProjectDefaultBranch`. A repo that releases off another branch, such as go-toolchain on `v1`, therefore gets a correct `latest` with no publisher cooperation.

buildhost authenticates these lookups as a **GitHub App** when one is configured. That needs `BUILDHOST_GITHUB_APP_ID` and `BUILDHOST_GITHUB_APP_PRIVATE_KEY`. The key is an inline PEM, real or env-`\n`-escaped and normalized by `config.resolvePEM`, or a file path. `auth.SetGitHubApp` signs an app JWT and exchanges it for a per-installation token, and both are cached. Those are short-lived `metadata:read` installation tokens with a high rate limit. It falls back to a static `BUILDHOST_GITHUB_TOKEN` PAT, and then to anonymous, which GitHub throttles to 60 requests per hour per IP. The release-create API still accepts an optional `default_branch` field as a manual override, for a non-GitHub publisher or a private repo buildhost cannot reach.

A push to a feature branch never hijacks `latest`. When the default branch has no published release yet, unqualified `latest` is not available. There is no fallback to newest-overall, which defeats the no-hijack guarantee. `db.GetLatestRelease` centralizes this in a single JOIN on `projects.default_branch`. dl, brew, apt, the web frontend, the OCI `latest` tag and the npm `latest` dist-tag therefore stay consistent. A per-branch download (`?branch=`) is unaffected.

## Security notes on the lookup

On a GitHub-OIDC write, buildhost resolves the repo's default branch through `GET api.github.com/repos/{owner}/{repo}` (`auth.GitHubDefaultBranch`). The `owner/repo` comes only from the **verified** OIDC subject. `validRepoPath` validates it before it goes into the fixed-host URL. There is therefore no SSRF surface and no injection surface. `validRefName` re-validates the returned branch before it is stored. The lookup is gated on the GitHub Actions issuer, so another OIDC provider triggers no call. It is best-effort: a failure leaves the existing default branch, and it never fails a publish. It is cached for 1h on a positive answer and 5m on a negative one, so it cannot hammer GitHub.

It authenticates as a **GitHub App** when `BUILDHOST_GITHUB_APP_ID` and `BUILDHOST_GITHUB_APP_PRIVATE_KEY` are set. `auth.SetGitHubApp` signs an app JWT with the configured private key and exchanges it for a short-lived per-installation token. Both are cached. Otherwise it uses a static `BUILDHOST_GITHUB_TOKEN` PAT, and otherwise anonymous. The App private key and the PAT only widen rate limits and reach a private repo. Neither is ever logged or echoed. A malformed key disables App auth and logs that, instead of a crash at startup.

An environment variable can escape an inline PEM's newlines to the literal `\n` sequence in transit. That is the common Docker and compose footgun. `config.resolvePEM` un-escapes it before the parse. Without that the key fails to parse, App auth is silently disabled, and a default-branch lookup degrades to anonymous. Anonymous 404s on a private repo, and `projects.default_branch` stays stuck at the `master` seed.

## Draft releases

`releases.draft` comes from `migrations/016_release_draft.sql`. A release created with `"draft":true` over REST, or with `buildhost publish --draft`, stays unpublished. It is therefore invisible to `latest`, to per-branch resolution, to brew, apt, npm and OCI, and to the web frontend. Every one of those filters on `published = 1`. `resolveVersion` still serves it by EXACT version. That combination already existed. The column exists to record INTENT.

Retention's `ListAbandonedReleases` sweeps an unpublished release past the cutoff as a partial or failed upload. Without the flag a deliberate draft is indistinguishable from one, and the sweep deletes it out from under its owner. The query now excludes `draft = 1`. `PublishRelease` clears the flag, because a release cannot be both a private build and part of the stream. The default is 0, so every existing release is untouched.
