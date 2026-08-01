# Static sites

Static site hosting: `internal/sites/`. Upload tar.gz (or zip) archives, serve
files per branch. Self-registering via `auth.OnReady()`.

Extracted verbatim from CLAUDE.md's `internal/sites/` entry, which had grown
into a manual; the apex-path section below is the only new prose.

## Serving schemes

Two schemes address the same sites, and both resolve their branch through the
same helpers, so a URL means the same file either way.

### Classic: `sites.{domain}/{project}/...`

- `/{project}/branch/{branch}/{path...}` -- an explicit branch.
- `/{project}/branches` -- the branch listing (gated; never public-read).
- `/{project}` -- the apex path: the project's root and any file under it, on
  the project's default branch. See below.
- `PUT`/`DELETE /{project}/branch/{branch}` -- deploy and remove.

### Apex path (`/{project}` and `/{project}/<file>`)

The **bare project root** (`/{project}` and `/{project}/`) `302`-redirects
(`Cache-Control: no-store`, since the target is a mutable pointer) to
`/{project}/branch/{default}/` -- the project's `default_branch` (learned from
GitHub on publish, e.g. `main`; seed default `master`, the same branch the apex
download `latest` tracks), so a project's root URL resolves to its canonical
site without the caller knowing which branch it lives on. When the resolved
`default_branch` has **no published site** (e.g. the GitHub-learned default lags
at the seed `master` while sites were only ever deployed to `main`, because
buildhost couldn't reach a private repo to learn its real default), the redirect
falls back to a branch that *does* have a site -- preferring the conventional
`main`/`master` names over a more-recently-updated ephemeral PR-preview branch,
then the newest site as a last resort -- so the root never bounces to a
guaranteed 404 (`resolveRootBranch` in `serve.go`, used by both the redirect and
its public-read gate so they stay consistent; with no sites at all it keeps the
default branch unchanged).

A **file path under the project root** (`/{project}/<file>`) serves that file
from the same resolved default branch (`ServeDefaultBranch` -> `serveSiteFile`,
the same tar scan / `index.html` / `404.html` handling every other site read
uses). Without it, only `/branch/{branch}/{path}` served a file and the bare
root merely redirected, so every link into a site had to name a branch it has no
business knowing -- an MCP App declaring a runner origin, a README, a
cross-project link. This is the grammar the `{project}.<site-domain>` scheme
already had for bare paths.

The project/path split is ambiguous by construction (project names are
slash-namespaced and file paths have slashes too), and the router cannot make
it: `{project}` has no wildcard after it, so it binds the WHOLE remainder
greedily. `parseRootRoute` splits it by **longest match against existing
projects** (`splitProjectPath`: segment prefixes longest -> shortest via
`GetProject`) -- the same git-refs-style shadowing rule `splitSiteBranch`
applies to slash-named branches. So with projects `org` and `org/repo`,
`/org/repo` stays org/repo's root even when `org`'s site holds a file named
`repo`: no URL that resolved before this route served files can be repointed by
it. When no prefix names a project the whole remainder stays the project name,
so `requireProject` answers exactly the 404 the bare-root route always did.

That resolution only ROUTES; it never grants. `requireProject` applies its
normal auth to whatever it resolves, and every candidate is a prefix of the
requested path on a route that already answers 401 for a private project, so no
name becomes discoverable that was not already.

The route is a literal-less `GET /{project}` that scores below the
`branch`/`branches` routes (router best-match), so it only catches paths that
aren't one of those and never shadows them; it is served publicly (via
`AllowsPublicRead`) exactly when the resolved branch's site is public, the same
rule as a single-branch read.

### Project site subdomains (`{project}.<site-domain>`)

`subdomain.go`: when `BUILDHOST_SITE_DOMAIN` is configured (config ->
`server.New` -> `auth.Init`; UNSET registers ZERO new routes -- pinned by
`TestSiteDomain_RouteTable`), each project whose name is a valid single DNS
label (`validSiteLabel`: `[a-z0-9-]`, 1..63, no leading/trailing hyphen; host
labels folded to lowercase like DNS) is ALSO served at
`{project}.<site-domain>/{path...}` (registered via `auth.SiteDomainHandle`
inside the sites `OnReady`, since config is unknown at `init()` -- the one
sanctioned exception to the routescheck `init()`-registration rule, and
therefore invisible to `buildhost routes`, which enumerates without booting).
The route is GET-only ReadAccess (never provisions); `parseSubdomainRoute`
builds the SAME `route` struct as the classic scheme (project = the host label
bound by the router's non-final `{project}` host param), so `AllowsPublicRead`
and the centralized `requireProject` flow apply verbatim.

Grammar: a bare path serves the **default branch** via `resolveRootBranch`
(`root=true` -- the exact chain the classic apex path and its gate share);
`~<branch>/<path>` serves any other branch behind the `~` sigil (outside the
branch charset, so no collision); `~<branch>` == the resolved default 302s
(`no-store`) to the canonical bare form (one canonical URL per file; the default
branch is a mutable pointer); a branch root missing its trailing slash 301s to
the slashed form. Slash-named branches resolve by **longest match** against
existing site rows (`splitSiteBranch`: segment prefixes longest->shortest via
`GetSite`, skipping candidates outside the branch charset -- the same
git-refs-style shadowing rule everywhere).

Reserved on the subdomain scheme only: the `~` sigil at path root, and the
literal `/__sso` (the cross-domain sign-in redemption endpoint, registered by
internal/auth inside the same host family because the `{project}.<site-domain>`
pattern's 2 literal host labels outrank every path term -- it claims ALL of
`*.<site-domain>` one label deep, including would-be service labels like `dl`,
and host-agnostic routes never serve on a claimed host; within the family the
literal `/__sso` outranks `{path...}`). The BARE site apex (2 labels) matches no
host-bearing route and falls through to host-agnostic routes (so
`<site-domain>/healthz` and `/__sso` answer there; the web+API surface does too
ONLY while `BUILDHOST_PRIMARY_DOMAIN` is unset -- with it set those routes are
primary-scoped and 404 off-apex, see the unknown-domain security note). Names
with `/`, `.`, `_` or over 63 chars 404 on the scheme and stay reachable on
`sites.{domain}/...` (no brew-style fold-back in v1).

## Public sites under private projects

A site uploaded with header `X-Public-Site: true` is stored with `is_public=1`
and served **without a token even under a private project** -- the sites read
route implements `auth.PublicReadAuthorizer` so the centralized
`requireProject` opens just that one branch (the project's releases/other
branches stay gated). Used for PR previews of private repos (the
`buildhost-publish-site` action sends the header when `public: true`).

## Storage and limits

Sites are uploaded as tar.gz (`Content-Type: application/gzip`) or zip
(`Content-Type: application/zip`). Both formats are stored as raw tar
internally and served by scanning tar headers per request. Each branch is an
independent deployment (one row in the `sites` table). Re-deploying a branch
replaces the previous site atomically. Upload size capped at 256 MiB, max
10,000 files per site. The project's own visibility is unchanged by a public
site (still derived from the OIDC `repository_visibility` claim and re-synced
on every write).

## Fix-forwards shipped with the subdomain scheme

- The classic GET path (`Serve` + its gate) previously bound `{branch}` to only
  the FIRST segment (router splits ascending before a wildcard and never
  backtracks on DB misses), so a slash-named branch uploaded via the greedy PUT
  bind 404'd on every fetch -- both schemes now share `splitSiteBranch`.
- `Upload` now rejects branches outside `[a-zA-Z0-9._/-]{1,256}`
  (`validSiteBranch`, mirroring api `validGitBranch`/auth `validRefName`) with a
  400 -- previously any bytes were stored.
