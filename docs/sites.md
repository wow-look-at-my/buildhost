# Static sites

Static site hosting: `internal/sites/`. Upload tar.gz (or zip) archives, serve files per branch. Self-registering via `init()`.

Three files carry the URL grammar, in the order a request meets them. `resolve.go` turns a `<ref>[/<path>]` remainder into the branch that serves it. `canonical.go` decides which URL is THE URL, and how every other spelling redirects toward it. `serve.go` streams the bytes.

This was extracted verbatim from CLAUDE.md's `internal/sites/` entry, which had grown into a manual. The apex-path section below is the only new prose.

## Serving schemes

Two schemes address the same sites, and both resolve their branch through the same helpers, so a URL means the same file either way.

### Classic: `sites.{domain}/{project}/...`

- `/{project}/<file>` -- **the canonical URL**: the project's own root path, served from its default branch. See below.
- `/{project}/@{ref}/{path...}` -- an explicit branch or commit. See below.
- `/{project}/branch/{branch}/{path...}` -- the original spelling. It `302`s to whichever spelling above names the same file.
- `/{project}/branches` -- the branch listing. It is gated, and never public-read.
- `PUT` and `DELETE` on `/{project}/@{branch}` -- deploy and remove. The `/branch/{branch}` spelling works too.

### Redirects only ever run toward the shorter URL

The bare project path is canonical. Every other spelling that means the same file redirects INTO it, never the other way round:

| request | result |
| --- | --- |
| `/{project}/<file>` | serves (canonical, no hop) |
| `/{project}` | `301` -> `/{project}/` (trailing slash only) |
| `/{project}/@{default}/<file>` | `302` no-store -> `/{project}/<file>` |
| `/{project}/@{other}/<file>` | serves (no shorter spelling exists) |
| `/{project}/@{commit}/<file>` | serves (see below -- never collapsed) |
| `/{project}/branch/{default}/<file>` | `302` no-store -> `/{project}/<file>` |
| `/{project}/branch/{other}/<file>` | `302` -> `/{project}/@{other}/<file>` |

The bare root used to `302` to `/{project}/branch/{default}/`. That was backwards, because it pointed the short, stable URL at the long one. It is gone. To name the default branch says nothing the bare path does not already say.

**Exactly one URL serves each file.** The legacy `/branch/` form is a pure redirect shim, in `RedirectLegacyBranch`. It is never a second place that serves bytes. There is therefore one serving implementation, and one canonical URL per file.

That form is still not going away. It is what every published preview link, README and deployed client already says. A `302` keeps every one of them working, because every HTTP client follows a redirect on GET. It resolves its branch the same way the `@` form does, through `splitSiteBranch`, so a slash-named branch still works. It names the resolved branch in its target, in ONE hop.

### The publish response says where the site is

The upload's `201` body is the stored row plus a `url` field. That field is the canonical URL this deployment is now served at. It is the bare project path when the deployment is on the default branch, and the `@` spelling otherwise. `canonicalSiteURL` calls the same `canonicalURLFor` the redirects use. The URL a publisher advertises and the URL the server considers canonical therefore cannot drift apart.

The field exists because they DID drift. Every publisher used to build the URL from the project and branch, which made each publisher a copy of the grammar. When the canonical form moved to the bare path, every publisher kept posting a `/branch/` link that from then on only redirected. A publisher must ask, and must not re-derive. `buildhost-publish-site` falls back to the `@` spelling against a server too old to send the field. That is the current grammar, and not the one that only redirects.

One thing deliberately does not collapse. A **commit** ref is the most specific spelling there is. To rewrite it to a mutable pointer throws the pin away. `refNamesBranch` gates the collapse.

The collapse is also skipped when the bare URL addresses a DIFFERENT project. `apexURLFor` decides that. With projects `org` and `org/repo`, `org`'s own file `repo/x.css` has no usable short URL, because `/org/repo/x.css` belongs to org/repo. That file is served in place, rather than redirected somewhere that means something else.

### The `@` ref sigil

`@` names the branch on both schemes. Those are `sites.{domain}/{project}/@{branch}/{path}` and `{project}.<site-domain>/@{branch}/{path}`. It replaces the classic scheme's `branch/` path segment and the subdomain scheme's `~` with one grammar. Both older forms keep working, as described below.

Each older form keeps working **only on the scheme it came from**. That asymmetry is easy to misread. `/branch/{branch}/` is classic-scheme only, and 302s to the canonical URL. `~{branch}` is subdomain-scheme only, and 301s to `@`.

`~` has never been a sigil on the classic scheme. `/{project}/~{branch}/<file>` is therefore an ordinary path, which names a literal file `~{branch}/<file>` under the default branch. It answers 404, or 401 on a private project whose root branch is not public. It never answers a redirect. `TestLegacySigil_*` in `internal/sites` pins that. The top-level CLAUDE.md once claimed `~` redirected on the classic scheme too, and sent a reader hunting a routing bug that does not exist.

A sigil beats a path segment for one reason. `branch` is an ordinary segment. The old form therefore failed to address a site with a top-level `branch/` directory at all. Every URL in such a site also carried a segment that reads like part of the site.

`@` is outside the branch charset (`validSiteBranch`) AND outside the project-name charset. It can therefore never be part of a name it separates. That makes the project and branch split exact, with no DB lookup, however deeply namespaced the project is. `splitBranchSigil` does it. `@` is also very rare in a real file name, which is what makes it safe to reserve at that one position.

What it does NOT delimit is where the branch ENDS. A branch name may contain `/`, as `claude/foo` does. `@claude/foo/c.html` is therefore still resolved by longest match against the project's site rows, in `splitSiteBranch`. The older spelling needs the same rule. Both spellings hand the same raw `<branch>[/<path>]` remainder to that one resolver, through `route.ref()`. They can therefore never disagree about which file a URL addresses.

A read shares the apex route. A sigil is not a path segment, so no pattern can express it without an out-score of the literal-less `GET /{project}`. `parseRootRoute` splits at the sigil when there is one. That is the same place the `{project}.<site-domain>` scheme has always kept its sigil grammar.

A write, meaning `PUT` or `DELETE`, does get its own patterns, and each gets two. `@{branch}` is one path segment, while a branch name may span several. The second pattern therefore binds the rest, and `parseSigilRoute` rejoins them.

No published URL breaks. `/{project}/branch/{branch}/...` keeps resolving, as a `302`. `TestBranchSigil_LegacyFormRedirects` pins that, and the `upload-artifact-action-e2e` CI job pins it over real HTTP with a real client.

Both publishers still EMIT that spelling for the upload endpoint itself, `PUT /{project}/branch/{branch}`. That is a write route an older server also accepts. It is not the URL they advertise afterwards. That URL now comes from the server, as "The publish response says where the site is" describes. A publisher that runs against a server older than this change falls back to the `@` spelling, rather than to one that only redirects.

Inside buildhost, every site LINK is in the `@` form. That covers the web frontend's per-branch links and the admin dashboard's "Open" links alike, and the dashboard builds its links through `siteBranchURL`. Both ship in the same binary as the server, so neither can outrun it.

The dashboard still shows `/branch/` in its endpoint table and its curl snippets, and it must. Those are the `PUT` and `DELETE` write routes. `TestAdminStaticSiteLinksUseRefSigil` tells the two apart. A link concatenates a runtime branch. An endpoint carries the literal `{branch}` placeholder.

### Commit refs (`@{commit}`)

`@` also takes a git commit. That is the full 40-hex sha, or any abbreviation of at least 7 characters, matched case-insensitively, as git does. `looksLikeCommit` decides. A link can therefore pin the exact build it was tested against, instead of a track of wherever the branch moves next.

    sites.{domain}/myapp/@0f1e2d3/runner.html

This needs nothing new from a publisher. Every deploy already records its commit, in `sites.git_commit`. That value comes from the `X-Git-Commit` header the CLI and the `buildhost-publish-site` action send, and it defaults to `github.sha`.

Resolution runs branches first, then commits, through `splitSiteBranch` and then `resolveCommitRef`. A branch whose name happens to be hex therefore always wins. No URL that resolved before can be repointed by a commit.

Here is exactly what a commit URL guarantees. A site is keyed `(project, branch)`, and a re-deploy replaces it in place. A commit therefore resolves only while it is still some branch's LIVE deployment. Re-deploy the branch and the old sha answers `404`.

That is the useful half of immutability. The URL serves that build or nothing, and it never quietly becomes a later one. It costs no retention of every historical deployment. When several branches sit on the same commit, the newest deployment wins, so the answer is deterministic. `SitesByCommitPrefix` orders by `updated_at DESC`.

### Apex path (`/{project}` and `/{project}/<file>`)

The **bare project root**, `/{project}/`, serves `index.html` straight from the project's `default_branch`. buildhost learns that branch from GitHub on publish, and it is often `main`. The seed default is `master`, which is the same branch the apex download `latest` tracks. A project's root URL therefore resolves to its canonical site, without the caller's knowledge of which branch it lives on, and with no redirect hop.

`/{project}` without the slash `301`s to `/{project}/`. A relative link in `index.html` then resolves under the project, rather than under the host root.

The resolved `default_branch` can have **no published site**. That happens when the GitHub-learned default lags at the seed `master` while sites were only ever deployed to `main`. buildhost hits that case when it fails to reach a private repo to learn its real default. The root then falls back to a branch that *does* have a site. It prefers the conventional `main` and `master` names over a more-recently-updated ephemeral PR-preview branch. It takes the newest site as a last resort. The root therefore never serves a guaranteed 404. `resolveRootBranch` in `serve.go` does this. The serve, the `@{default}` collapse and the public-read gate all use it, so they stay consistent. With no sites at all it keeps the default branch unchanged.

A **file path under the project root**, `/{project}/<file>`, serves that file from the same resolved default branch. `ServeDefaultBranch` calls `serveSiteFile` for it, which is the same tar scan, `index.html` and `404.html` handling every other site read uses.

Without that route, only an explicit branch URL served a file, and the bare root only redirected. Every link into a site therefore had to name a branch it has no business knowing. An MCP App that declares a runner origin, a README, and a cross-project link all had that problem. This is the grammar the `{project}.<site-domain>` scheme already had for a bare path.

The project and path split is ambiguous by construction, because a project name is slash-namespaced and a file path has slashes too. The router cannot make that split. `{project}` has no wildcard after it, so it binds the WHOLE remainder greedily.

`parseRootRoute` splits it by **longest match against existing projects**. `splitProjectPath` tries segment prefixes from longest to shortest, through `GetProject`. That is the same git-refs-style shadowing rule `splitSiteBranch` applies to a slash-named branch. With projects `org` and `org/repo`, `/org/repo` therefore stays org/repo's root, even when `org`'s site holds a file named `repo`. No URL that resolved before this route served files can be repointed by it. When no prefix names a project, the whole remainder stays the project name. `requireProject` then answers exactly the 404 the bare-root route always did.

That resolution only ROUTES. It never grants. `requireProject` applies its normal auth to whatever it resolves. Every candidate is a prefix of the requested path, on a route that already answers 401 for a private project. No name therefore becomes discoverable that was not discoverable already.

The route is a literal-less `GET /{project}`. It scores below the `branch` and `branches` routes under the router's best-match rule. It therefore catches only a path that is neither of those, and it never shadows them. It is served publicly, through `AllowsPublicRead`, exactly when the resolved branch's site is public. That is the same rule as a single-branch read.

### Project site subdomains (`{project}.<site-domain>`)

`subdomain.go` implements this. It activates when `BUILDHOST_SITE_DOMAIN` is configured, through config to `server.New` to `auth.Init`. An UNSET value registers ZERO new routes, which `TestSiteDomain_RouteTable` pins.

Each project whose name is a valid single DNS label is ALSO served at `{project}.<site-domain>/{path...}`. `validSiteLabel` decides validity: `[a-z0-9-]`, 1 to 63 characters, with no leading or trailing hyphen. A host label is folded to lowercase, as DNS does.

The route is registered through `auth.SiteDomainHandle`, from an `auth.OnSiteDomain` hook, because the domain is unknown at `init()`. `buildhost routes` runs the same hook against `auth.SiteDomainPlaceholder`. The route is therefore still enumerable, and it still shows up in a PR's route diff.

The route is GET-only ReadAccess, and it never provisions. `parseSubdomainRoute` builds the SAME `route` struct as the classic scheme. The project is the host label the router's non-final `{project}` host parameter binds. `AllowsPublicRead` and the centralized `requireProject` flow therefore apply verbatim.

The grammar follows. A bare path serves the **default branch**, through `resolveRootBranch` with `root=true`. That is the exact chain the classic apex path and its gate share. `@<branch>/<path>` serves any other branch behind the `@` sigil, which sits outside the branch charset, so there is no collision.

`@<branch>` naming the resolved default 302s, with `no-store`, to the canonical bare form. There is one canonical URL per file. The default branch is a mutable pointer. `~<branch>` is this scheme's original sigil, and it 301s to the `@` spelling, so no published URL breaks. A branch root that is missing its trailing slash 301s to the slashed form.

A slash-named branch resolves by **longest match** against the existing site rows. `splitSiteBranch` tries segment prefixes from longest to shortest, through `GetSite`, and skips a candidate outside the branch charset. That is the same git-refs-style shadowing rule as everywhere else.

Some things are reserved on the subdomain scheme only. They are the `@` and `~` sigils at the path root, and the literal `/__sso`.

`/__sso` is the cross-domain sign-in redemption endpoint. `internal/auth` registers it inside the same host family. The literal host labels in the `{project}.<site-domain>` pattern outrank every path term. That pattern therefore claims ALL of `*.<site-domain>` one label deep. That claim covers a service label such as `dl` too. A host-agnostic route never serves on a claimed host. Within the family, the literal `/__sso` outranks `{path...}`.

The BARE site apex carries no project label. It matches no host-bearing route, and falls through to the host-agnostic routes. `<site-domain>/healthz` and `/__sso` therefore answer there. The web and API surface answers there too, but ONLY while `BUILDHOST_PRIMARY_DOMAIN` is unset. With that value set, those routes are primary-scoped and answer 404 off-apex. See the unknown-domain security note.

A name with `/`, `.` or `_`, or a name over 63 characters, answers 404 on this scheme. It stays reachable on `sites.{domain}/...`. There is no brew-style fold-back in v1.

## CORS applies to REDIRECTS, not just to the bytes

`setSiteSecurityHeaders`, in `internal/sites/serve.go`, drops the app's strict `Content-Security-Policy` and `X-Frame-Options`. It sets the hosted-site headers instead, and `Access-Control-Allow-Origin: *` is among them. **Every site response must go through it. That includes a redirect, and it must happen BEFORE the redirect is written.**

A browser re-checks CORS on each hop of a cross-origin fetch. A redirect that omits the header therefore fails the whole load, even when its target carries it. The browser reports the failure against the ORIGINAL URL, so the 200 at the end of the chain looks innocent. `curl` enforces CORS at no hop. A redirect chain it follows happily can therefore be completely unusable from a page.

This is not hypothetical. The legacy `/{project}/branch/{branch}/` form became a redirect to the canonical spelling. `RedirectLegacyBranch` did not call the helper at all. `ServeDefaultBranch` called it *after* it wrote its root-trailing-slash redirect.

The legacy form is what every deployed client, README and published preview link still says. Every cross-origin consumer of every hosted site therefore broke at once. An admin dashboard that imported an ES module from `sites.pazer.build` sat retrying `error loading dynamically imported module` on every page view, while the same URL returned 200 to curl.

Two checks guard it. Both were verified to fail before the fix.

- `internal/sites/cors_test.go` -- every redirect either scheme can emit must carry the header. Add a case here when you add a redirect.
- CI job `sites-cors-e2e`, which runs `test/dats/sites-cors.dats`. It spawns a real server. It walks each redirect chain, and asserts the header on every hop. It then has a **real headless browser** import a module cross-origin, through the legacy redirect. The browser layer is the point. It also covers the MIME type and CSP, which a header assertion cannot see. It FAILS when no browser can be launched, rather than a skip, because a check that cannot go red is decoration.

## Public sites under private projects

A site uploaded with the header `X-Public-Site: true` is stored with `is_public=1`. It is served **without a token, even under a private project**. The sites read route implements `auth.PublicReadAuthorizer`, so the centralized `requireProject` opens just that one branch. The project's releases and its other branches stay gated. This is used for a PR preview of a private repo. The `buildhost-publish-site` action sends the header when its `public` input is `true`.

## Storage and limits

A site is uploaded as tar.gz, with `Content-Type: application/gzip`, or as zip, with `Content-Type: application/zip`. It is stored as an indexed binpazer archive, in `internal/binarchive`. To serve one file is therefore a directory lookup plus a block decode, and not a scan of the whole tar. A pre-archive blob is detected by its magic and served through the old scan. See `docs/site-archives.md`.

The branch routes and the apex path both go through `serveSiteFile`, so both get the indexed read. Each branch is an independent deployment. It holds one row in the `sites` table. A re-deploy of a branch replaces the previous site atomically. The upload size is capped at 256 MiB, and a site holds at most 10,000 files. A public site does not change the project's own visibility. That visibility still comes from the OIDC `repository_visibility` claim. It is re-synced on every write.

## Fix-forwards shipped with the subdomain scheme

- The classic GET path, `Serve` plus its gate, previously bound `{branch}` to only the FIRST segment. The router splits ascending before a wildcard, and it never backtracks on a DB miss. A slash-named branch uploaded through the greedy PUT bind therefore answered 404 on every fetch. Both schemes now share `splitSiteBranch`.
- `Upload` now rejects a branch outside `[a-zA-Z0-9._/-]{1,256}` with a 400. `validSiteBranch` enforces that, and it mirrors `validGitBranch` in api and `validRefName` in auth. Any bytes were stored before.
