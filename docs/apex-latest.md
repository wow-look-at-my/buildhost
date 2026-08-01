# Apex `latest` and the per-project default branch

Extracted verbatim from CLAUDE.md; paragraph breaks were added at the existing
topic boundaries, no wording changed.

Apex `latest` (a download with no `?v=` and no `?branch=` -- e.g.
`dl/{project}/latest/{os}/{arch}`) resolves to the newest published release on the
project's **default branch** -- a per-project field (`projects.default_branch`,
`migrations/011_project_default_branch.sql`) that defaults to `master`
(`db.LatestBranch`).

buildhost learns the real default branch **itself, with nothing sent in the
publish**: on a GitHub-OIDC write the centralized `requireProject` reads
`owner/repo` from the verified OIDC subject (`repo:OWNER/REPO:...`) and asks
GitHub for that repo's default branch (`auth.GitHubDefaultBranch` -> `GET
api.github.com/repos/{owner}/{repo}`, best-effort + cached, gated on the GitHub
Actions issuer), then records it via `db.SetProjectDefaultBranch` -- so a repo that
releases off another branch (e.g. go-toolchain on `v1`) gets a correct `latest`
without any publisher cooperation.

buildhost authenticates these lookups as a **GitHub App** when configured
(`BUILDHOST_GITHUB_APP_ID` + `BUILDHOST_GITHUB_APP_PRIVATE_KEY`, inline PEM --
real or env-`\n`-escaped, normalized by `config.resolvePEM` -- or a file path;
`auth.SetGitHubApp` -> app JWT -> per-installation token, cached): short-lived
`metadata:read` installation tokens with a high rate limit. It falls back to a
static `BUILDHOST_GITHUB_TOKEN` PAT, then to anonymous (throttled 60 req/hr/IP).
The release-create API still accepts an optional `default_branch` field as a
manual override (non-GitHub publishers / private repos buildhost can't reach).

A push to a feature branch never hijacks `latest`. When the default branch has no
published release yet, unqualified `latest` is not available (no fallback to
newest-overall, which would defeat the no-hijack guarantee). Centralized in
`db.GetLatestRelease` (a single JOIN on `projects.default_branch`), so
dl/brew/apt/web, the OCI `latest` tag, and the npm `latest` dist-tag stay
consistent. Per-branch downloads (`?branch=`) are unaffected.

## Security notes on the lookup

On a GitHub-OIDC write, buildhost resolves the repo's default branch via `GET
api.github.com/repos/{owner}/{repo}` (`auth.GitHubDefaultBranch`). The
`owner/repo` comes only from the **verified** OIDC subject and is validated
(`validRepoPath`) before being interpolated into the fixed-host URL, so there is
no SSRF or injection surface; the returned branch is re-validated (`validRefName`)
before it is stored. The lookup is gated on the GitHub Actions issuer (no call for
other OIDC providers), best-effort (a failure leaves the existing default branch
-- it never fails a publish), and cached (1h positive / 5m negative) so it cannot
hammer GitHub.

Authenticated as a **GitHub App** (`auth.SetGitHubApp`: app JWT signed with the
configured private key -> short-lived per-installation token, both cached) when
`BUILDHOST_GITHUB_APP_ID`/`BUILDHOST_GITHUB_APP_PRIVATE_KEY` are set, else a
static `BUILDHOST_GITHUB_TOKEN` PAT, else anonymous. The App private key / PAT only
widen rate limits and reach private repos; they are never logged or echoed, and a
malformed key disables App auth (logged) rather than crashing startup. An inline
PEM whose newlines were escaped to the literal `\n` sequence in transit through an
environment variable (the common Docker/compose footgun) is un-escaped by
`config.resolvePEM` before parsing -- otherwise the key fails to parse, App auth is
silently disabled, and default-branch lookups degrade to anonymous, which 404s on a
private repo and leaves `projects.default_branch` stuck at the `master` seed.

## Draft releases

`releases.draft`, `migrations/016_release_draft.sql`: a release created with
`"draft":true` (REST) or `buildhost publish --draft` stays unpublished, so it is
invisible to `latest`, per-branch resolution, brew/apt/npm/OCI and the web
frontend -- every one of those filters `published = 1` -- while `resolveVersion`
still serves it by EXACT version. That combination already existed; the column
exists to record INTENT. Retention's `ListAbandonedReleases` sweeps unpublished
releases past the cutoff as partial/failed uploads, and without the flag a
deliberate draft was indistinguishable from one, so it would be deleted out from
under its owner; the query now excludes `draft = 1`. `PublishRelease` clears the
flag (a release cannot be both a private build and part of the stream). Default 0,
so every existing release is untouched.
