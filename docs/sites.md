# Static sites

Static site hosting: `internal/sites/`. Upload tar.gz (or zip) archives, serve
files per branch. Self-registering via `auth.OnReady()`.

Three files carry the URL grammar, in the order a request meets them:
`resolve.go` (a `<ref>[/<path>]` remainder -> the branch serving it),
`canonical.go` (which URL is THE URL, and how every other spelling redirects
toward it), `serve.go` (streaming the bytes).

Extracted verbatim from CLAUDE.md's `internal/sites/` entry, which had grown
into a manual; the apex-path section below is the only new prose.

## Serving schemes

Two schemes address the same sites, and both resolve their branch through the
same helpers, so a URL means the same file either way.

### Classic: `sites.{domain}/{project}/...`

- `/{project}/<file>` -- **the canonical URL**: the project's own root path,
  served from its default branch. See below.
- `/{project}/@{ref}/{path...}` -- an explicit branch or commit. See below.
- `/{project}/branch/{branch}/{path...}` -- the original spelling; `302`s to
  whichever of the two above names the same file.
- `/{project}/branches` -- the branch listing (gated; never public-read).
- `PUT`/`DELETE /{project}/@{branch}` (and the `/branch/{branch}` spelling) --
  deploy and remove.

### Redirects only ever run toward the shorter URL

The bare project path is canonical. Every other spelling that means the same
file redirects INTO it, never the other way round:

| request | result |
| --- | --- |
| `/{project}/<file>` | serves (canonical, no hop) |
| `/{project}` | `301` -> `/{project}/` (trailing slash only) |
| `/{project}/@{default}/<file>` | `302` no-store -> `/{project}/<file>` |
| `/{project}/@{other}/<file>` | serves (no shorter spelling exists) |
| `/{project}/@{commit}/<file>` | serves (see below -- never collapsed) |
| `/{project}/branch/{default}/<file>` | `302` no-store -> `/{project}/<file>` |
| `/{project}/branch/{other}/<file>` | `302` -> `/{project}/@{other}/<file>` |

The bare root used to `302` to `/{project}/branch/{default}/`. That was
backwards -- it pointed the short, stable URL at the long one -- and it is gone:
naming the default branch says nothing the bare path doesn't already say.

**Exactly one URL serves each file.** The legacy `/branch/` form is a pure
redirect shim (`RedirectLegacyBranch`), never a second place that serves bytes,
so there is one serving implementation and one canonical URL per file. It is
still not going away -- it is what every published preview link, README and
deployed client already says, and a `302` keeps every one of them working, since
all HTTP clients follow redirects on GET. It resolves its branch the same way
the `@` form does (`splitSiteBranch`, so a slash-named branch still works) and
names the resolved branch in its target, in ONE hop.

### The publish response says where the site is

The upload's `201` body is the stored row plus a `url` field: the canonical URL
this deployment is now served at -- the bare project path when it is on the
default branch, else the `@` spelling. `canonicalSiteURL` is the same
`canonicalURLFor` the redirects use, so the URL a publisher advertises and the
URL the server considers canonical cannot drift apart.

The field exists because they DID drift. Every publisher used to build the URL
from the project and branch, which made each one a copy of the grammar; when the
canonical form moved to the bare path, they all kept posting `/branch/` links
that from then on only redirected. A publisher should ask, not re-derive.
(`buildhost-publish-site` falls back to the `@` spelling against a server too old
to send the field -- the current grammar, not the one that only redirects.)

One thing deliberately does not collapse: a **commit** ref is the most specific
spelling there is, so rewriting it to a mutable pointer would throw the pin away
(`refNamesBranch` gates the collapse).

The collapse is also skipped when the bare URL would address a DIFFERENT project
(`apexURLFor`): with projects `org` and `org/repo`, `org`'s own file `repo/x.css`
has no usable short URL, because `/org/repo/x.css` belongs to org/repo. It is
served in place rather than redirected somewhere that means something else.

### The `@` ref sigil

`@` names the branch on both schemes:
`sites.{domain}/{project}/@{branch}/{path}` and
`{project}.<site-domain>/@{branch}/{path}`. It replaces two different spellings
-- the classic scheme's `branch/` path segment and the subdomain scheme's `~`
-- with one grammar, and both older forms keep working (see below).

Each older form keeps working **only on the scheme it came from**, and the
asymmetry is easy to misread: `/branch/{branch}/` is classic-scheme only (302 to
the canonical URL), `~{branch}` is subdomain-scheme only (301 to `@`). `~` has
never been a sigil on the classic scheme, so `/{project}/~{branch}/<file>` is an
ordinary path naming a literal file `~{branch}/<file>` under the default branch:
it answers 404, or 401 on a private project whose root branch is not public --
never a redirect. Pinned by `TestLegacySigil_*` in `internal/sites`, because the
top-level CLAUDE.md once claimed `~` redirected on the classic scheme too and
sent a reader hunting a routing bug that does not exist.

Why a sigil rather than a path segment: `branch` is an ordinary segment, so a
site with a top-level `branch/` directory could not be addressed at all through
the old form, and every URL in it carried a segment that reads like part of the
site. `@` is outside the branch charset (`validSiteBranch`) AND the project-name
charset, so it can never be part of a name it separates -- which makes the
project/branch split exact, with no DB lookup, however deeply namespaced the
project is (`splitBranchSigil`). It is also vanishingly rare in real file names,
which is what makes it safe to reserve at that one position.

What it does NOT delimit is where the branch ENDS: branch names may contain `/`
(`claude/foo`), so `@claude/foo/c.html` is still resolved by longest match
against the project's site rows (`splitSiteBranch`) -- the same rule the older
spelling needs. Both spellings hand the same raw `<branch>[/<path>]` remainder
(`route.ref()`) to that one resolver, so they can never disagree about which
file a URL addresses.

Reads share the apex route: a sigil is not a path segment, so no pattern can
express it without out-scoring the literal-less `GET /{project}`, and
`parseRootRoute` splits at the sigil when there is one. That is the same place
the `{project}.<site-domain>` scheme has always kept its sigil grammar. Writes
(`PUT`/`DELETE`) do get their own patterns, two each -- `@{branch}` is one path
segment while a branch name may span several, so the second binds the rest and
`parseSigilRoute` rejoins them.

No published URL breaks. `/{project}/branch/{branch}/...` keeps resolving --
as a `302`, pinned by `TestBranchSigil_LegacyFormRedirects` and, over real HTTP
with a real client, by the `upload-artifact-action-e2e` CI job. What both
publishers still EMIT that spelling for is the upload endpoint itself
(`PUT /{project}/branch/{branch}`), which is a write route that older servers
also accept -- not the URL they advertise afterwards. That one now comes from
the server (see "The publish response says where the site is"), so a publisher
running against a server older than this change falls back to the `@` spelling
rather than one that only redirects.

### Commit refs (`@{commit}`)

`@` also takes a git commit -- the full 40-hex sha or any abbreviation of at
least 7 characters, case-insensitively, like git (`looksLikeCommit`). So a link
can pin the exact build it was tested against instead of tracking wherever the
branch moves next:

    sites.{domain}/myapp/@0f1e2d3/runner.html

This needs nothing new from publishers: every deploy already records its commit
(`sites.git_commit`, from the `X-Git-Commit` header the CLI and the
`buildhost-publish-site` action send, defaulted to `github.sha`).

Resolution order is branches first, then commits (`splitSiteBranch` ->
`resolveCommitRef`), so a branch whose name happens to be hex always wins and no
URL that resolved before can be repointed by a commit.

What a commit URL guarantees, exactly: sites are keyed `(project, branch)` and
replaced in place on re-deploy, so a commit resolves only while it is still some
branch's LIVE deployment. Re-deploy the branch and the old sha `404`s. That is
the useful half of immutability -- the URL serves that build or nothing, and
never quietly becomes a later one -- without retaining every historical
deployment. When several branches sit on the same commit the newest deployment
wins, so the answer is deterministic (`SitesByCommitPrefix` orders by
`updated_at DESC`).

### Apex path (`/{project}` and `/{project}/<file>`)

The **bare project root** (`/{project}/`) serves `index.html` straight from the
project's `default_branch` (learned from GitHub on publish, e.g. `main`; seed
default `master`, the same branch the apex download `latest` tracks), so a
project's root URL resolves to its canonical site without the caller knowing
which branch it lives on -- and without a redirect hop. `/{project}` without the
slash `301`s to `/{project}/` so relative links in `index.html` resolve under the
project rather than the host root. When the resolved
`default_branch` has **no published site** (e.g. the GitHub-learned default lags
at the seed `master` while sites were only ever deployed to `main`, because
buildhost couldn't reach a private repo to learn its real default), it falls
back to a branch that *does* have a site -- preferring the conventional
`main`/`master` names over a more-recently-updated ephemeral PR-preview branch,
then the newest site as a last resort -- so the root never serves a guaranteed
404 (`resolveRootBranch` in `serve.go`, used by the serve, the `@{default}`
collapse and the public-read gate alike so they stay consistent; with no sites
at all it keeps the default branch unchanged).

A **file path under the project root** (`/{project}/<file>`) serves that file
from the same resolved default branch (`ServeDefaultBranch` -> `serveSiteFile`,
the same tar scan / `index.html` / `404.html` handling every other site read
uses). Without it, only an explicit branch URL served a file and the bare
root only redirected, so every link into a site had to name a branch it has no
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
`@<branch>/<path>` serves any other branch behind the `@` sigil (outside the
branch charset, so no collision); `@<branch>` == the resolved default 302s
(`no-store`) to the canonical bare form (one canonical URL per file; the default
branch is a mutable pointer); `~<branch>` -- this scheme's original sigil --
301s to the `@` spelling, so no published URL breaks; a branch root missing its trailing slash 301s to
the slashed form. Slash-named branches resolve by **longest match** against
existing site rows (`splitSiteBranch`: segment prefixes longest->shortest via
`GetSite`, skipping candidates outside the branch charset -- the same
git-refs-style shadowing rule everywhere).

Reserved on the subdomain scheme only: the `@` and `~` sigils at path root,
and the literal `/__sso` (the cross-domain sign-in redemption endpoint, registered by
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

## CORS applies to REDIRECTS, not just to the bytes

`setSiteSecurityHeaders` (`internal/sites/serve.go`) drops the app's strict
`Content-Security-Policy`/`X-Frame-Options` and sets the hosted-site headers,
`Access-Control-Allow-Origin: *` among them. **Every site response must go
through it -- redirects included, and BEFORE the redirect is written.**

A browser re-checks CORS on each hop of a cross-origin fetch. A redirect that
omits the header therefore fails the whole load even when its target carries
it, and the browser reports the failure against the ORIGINAL URL, so the 200
at the end of the chain looks innocent. `curl` does not enforce CORS at any
hop, so a redirect chain it follows happily can be completely unusable from a
page.

This is not hypothetical. When the legacy `/{project}/branch/{branch}/` form
became a redirect to the canonical spelling, `RedirectLegacyBranch` did not
call the helper at all and `ServeDefaultBranch` called it *after* writing its
root-trailing-slash redirect. Since the legacy form is what every deployed
client, README and published preview link still says, every cross-origin
consumer of every hosted site broke at once -- an admin dashboard importing an
ES module from `sites.pazer.build` sat retrying `error loading dynamically
imported module` on every page view, while the same URL returned 200 to curl.

Two checks guard it, and both were verified to fail before the fix:

- `internal/sites/cors_test.go` -- every redirect either scheme can emit must
  carry the header. Add a case here when you add a redirect.
- CI job `sites-cors-e2e` (`.github/scripts/sites-cors-e2e.ts`) -- spawns a
  real server, walks each redirect chain asserting the header on every hop,
  then has a **real headless browser** import a module cross-origin through
  the legacy redirect. The browser layer is the point: it also covers MIME
  type and CSP, which header assertions cannot see. It FAILS when no browser
  can be launched rather than skipping, because a check that cannot go red is
  decoration.

## Public sites under private projects

A site uploaded with header `X-Public-Site: true` is stored with `is_public=1`
and served **without a token even under a private project** -- the sites read
route implements `auth.PublicReadAuthorizer` so the centralized
`requireProject` opens just that one branch (the project's releases/other
branches stay gated). Used for PR previews of private repos (the
`buildhost-publish-site` action sends the header when `public: true`).

## Storage and limits

Sites are uploaded as tar.gz (`Content-Type: application/gzip`) or zip
(`Content-Type: application/zip`) and stored as an indexed binpazer archive
(`internal/binarchive`), so serving one file is a directory lookup plus a block
decode rather than a scan of the whole tar; a pre-archive blob is detected by
magic and served through the old scan. See `docs/site-archives.md`. Both the
branch routes and the apex path go through `serveSiteFile`, so both get the
indexed read. Each branch is an
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
