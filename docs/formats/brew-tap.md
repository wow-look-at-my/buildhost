# Homebrew formulas and the generated tap

`internal/brew/`. Extracted verbatim from CLAUDE.md; paragraph breaks were added
at the existing topic boundaries, no wording changed.

## Routes

`brew.{domain}/tap.git` is a first-class cloneable tap URL (its smart-HTTP pair
is served directly; the bare `/tap.git` path and the dumb file paths still 301
ANONYMOUS requests to the equally cloneable `git.{domain}/brew/tap.git`);
formulas are also fetchable at `brew.{domain}/Formula/{project}.rb` and the
legacy `brew.{domain}/{project}` -- and because tap formula FILENAMES fold the
slash namespace (`tapFormulaName`: `gcc/pgo` -> `gcc-pgo.rb`), the formula routes
resolve a folded name back to its project when no project matches it literally
(`Handler.parseRoute`; a literally named project always wins, fold candidates are
restricted to projects the request may read -- `auth.TokenCanReadProject`, the
tap-membership rule -- so an anonymous probe of a private project's folded name
stays a 404 rather than a 401 existence leak, and requireProject still applies
its normal auth to the resolved project).

## Authenticated tap

`brew.{domain}/private/tap.git`: challenges anonymous requests with a 401 +
Basic (git does NOT send URL-embedded credentials preemptively -- it waits for a
challenge -- so a 200 would make a credentialed `brew tap x:TOKEN@...` silently
ingest the public-only tap), then serves a tap scoped to the credential: all
public projects plus the private projects the token can read
(`auth.TokenCanReadProject`, the same rule requireProject applies to
single-project reads, so tap membership can never leak a name the token couldn't
read directly). A credentialed request to plain `/tap.git` is likewise served in
place instead of redirected (a redirect would drop the credential mid-flight).
Private formulas `require_relative` the tap's `lib/buildhost_private_download.rb`
(`repackage.BrewPrivateStrategy`) and download `using:
BuildhostCurlDownloadStrategy`, which sends `Authorization: Bearer
$HOMEBREW_BUILDHOST_TOKEN` (HOMEBREW_-prefixed because Homebrew scrubs all other
env vars before formula code runs); the tap itself never embeds a token, and the
authenticated dl redirect completes the chain with its signed-token Location.
Credentialed tap/formula responses are `Cache-Control: private, no-store` +
`Vary: Authorization` so the CDN can never serve one scope's tap to another.

## Formula codegen must always emit valid, loadable Ruby

The class name is `repackage.BrewClassName`, which mirrors Homebrew's own
filename->class derivation (`Formulary.class_s`: `-`, `_`, `.` -- and buildhost's
folded `/` -- separate, next char upcased; `go1.2.3` -> `Go123`), and a
digit-leading project name is excluded from brew entirely via
`repackage.BrewEligibleProjectName` (formula endpoints 404, tap skips it) because
`class 7zip < Formula` is a Ruby syntax error and no substitute class satisfies
brew's loader (`Formulary.class_s("7zip") == "7zip"`, not a legal constant; both
measured against Homebrew 6.0.9).

**Every formula carries a TOP-LEVEL `url`+`sha256`** (the canonical resource --
linux/intel when present, else first in stable (OS, Arch) order;
`repackage.brewCanonicalResource`) in addition to the per-platform
`on_<os>`/`on_<arch>` stanzas, because Homebrew must find a stable URL on EVERY
platform to import a formula at all ("formula requires at least a URL" otherwise
-- and one failed import poisons whole-tap evaluation, so a linux-only project
used to break the tap for every macOS user); the on_* blocks still override
url/sha256 on platforms they match, and a formula whose resources span only ONE
os additionally declares `depends_on :linux`/`:macos` (homebrew-core's
single-platform pattern) so a foreign-platform install fails cleanly instead of
fetching a binary that cannot run.

## Opt-in `service do` block

`projects.create_service`, migration 013 -- the packaging-agnostic
background-service setting, DECLARED by the publishing repo's CI as an optional
`create_service` bool on release-create (the
`buildhost-create-release`/`buildhost-publish` actions' `create_service` input;
asserted idempotently on EVERY publish, absent = stored setting untouched so an
old CI never clobbers it -- the default_branch reassertion precedent), or flipped
directly by an operator via `PATCH /api/v1/projects/{project}`; either way only a
BOOL crosses the wire, so no publisher-controlled Ruby can enter the template: a
flagged binary-kind project's formula gains a `service do` block -- `run
[opt_bin/"<InstallName>"]` (opt path survives upgrades), `keep_alive
successful_exit: false` (launchd `KeepAlive {SuccessfulExit: false}`, CRASH-ONLY
restart: plain `keep_alive true` would respawn a deliberately-exiting app, e.g. a
single-instance exit-0 handoff, every ~10s), `log_path`/`error_log_path` under
`var/"log/"` (brew services mkpaths the log parents itself before load --
`Service#path_dirs`), `process_type :interactive` -- so ONE one-time `brew
services start <tap>/<project>` manages the binary as a login service thereafter
(macOS user LaunchAgent in the gui domain; upgrades keep it running via opt_bin).

Install-time auto-start is STRUCTURALLY impossible in a formula -- post_install
(the only install hook) runs inside brew's seatbelt sandbox whose profile is a
global `(deny file-write*)` plus a build-path allowlist (formula_installer.rb
post_install + extend/os/mac/sandbox.rb SEATBELT_ERB), so `~/Library/LaunchAgents`
is unwritable and a nested `brew services start` cannot work; a failing
post_install also sets Homebrew.failed (red install) -- never ship one. `brew
uninstall` does NOT stop services (Utils::Service is consumed only by
caveats.rb): document `brew services stop` before removal. On Linux brew's
systemd user units carry no graphical-session ordering/env, documented as a poor
GUI-app fit (the deb materialization is the Linux path -- see
internal/repackage). Non-binary kinds never emit the block (it references
`opt_bin`, which only `bin.install` stages), and the flag-off rendering is pinned
BYTE-IDENTICAL to the pre-flag output (formula bytes feed the tap's
content-addressed git objects -- off-state drift would mint a spurious tap commit
for every project).

Slash-named projects `bin.install` the BASENAME -- the tar.gz's only top-level
entry is the namespace dir and brew strips a lone top-level dir when unpacking,
so the staged file is just the basename; installing the slashed path ENOENTs.

## Installed file modes

**A binary-kind formula pairs `chmod 0755, bin/"<InstallName>"` with `skip_clean
"bin"`**: Homebrew's Cleaner rewrites the mode of everything under `bin`
regardless of what the formula installed
(`Library/Homebrew/extend/os/{linux,mac}/cleaner.rb` -- `0555` for a file it
recognizes as executable, i.e. a `#!` script, an ELF, or a Mach-O; `0444` for
anything else), so a Cosmopolitan/APE binary such as go-toolchain was installed
`0444` and could not be executed at all -- and `0555` is no better, because an APE
assimilates itself (rewrites its own file into native ELF/Mach-O) on first run
and dies with "cannot create <path>: Permission denied" without the write bit.
`skip_clean` prunes the Cleaner for `bin` (`Formula#skip_clean?` -> `Find.prune`),
so the installed `0755` survives; the mode the tar.gz ships (`0755` for
`kind=binary`) is irrelevant to brew either way. Non-binary kinds stage nothing
under `bin` and keep the default cleanup. Verified against Homebrew 6.x on Linux
end to end: `0444` before, `0755` + a successful self-assimilating run after.

## Digest cache

Formula download URLs point to tar.gz artifacts and sha256 values are computed
from the same tar.gz payload Homebrew downloads -- and are **cached in
`packaged_artifacts` under `format="tar.gz"`** (`formula.go` `tarGZSHA256`: lazy
fill on first request, then a DB read instead of a full repackage+hash per
artifact per request). The row is a digest cache only -- `storage_key` records the
SOURCE artifact blob, no tar.gz is stored, downloads still repackage on demand --
which is sound because tar.gz generation is deterministic per artifact (fixed tar
header name/size/mode with zero mtimes, fixed gzip header, content-addressed
input; pinned by `TestTarGZGenerationDeterministic`), and the rows ride the
existing retention cascade (`deleteReleaseRows` drops them with their artifacts).

## Tap git history

The tap itself is served from a **persistent, append-only, per-lineage git
history** under `{DataDir}/brew-tap/<sha256(key)>/` (`taphistory.go`; NOT under
the swept `{DataDir}/tmp` -- same durable-state precedent as
`apt-signing.key`/`download-signing.key`), one lineage per **(request-derived base
URL, credential scope)** key -- the base URL because a tap must never be served
with another host's URLs baked in, the credential scope (`tapScopeKey`: anon /
DB-token ID / OIDC subject+namespace) because tap contents depend on what the
credential may read, so one scope's lineage can never be handed to another. Each
lineage is a bare dumb-HTTP layout (loose `objects/`, `refs/heads/main`,
`info/refs`, `HEAD`) served by mmap through an `os.Root` (the storage-layer
pattern; no heap buffering, no path escape).

**Refs only ever fast-forward**: a rebuild reads the persisted tip, REUSES it when
the new content's tree is unchanged (no growth from periodic rebuilds -- the
commit sha is deterministic: zero timestamps, fixed identity, content-derived),
and otherwise appends a commit `parent <tip>` -- objects are written
(content-addressed, temp+rename) BEFORE the ref advances, so a reader never sees a
ref naming missing objects and a crash leaves a consistent store. This is what
keeps Homebrew's updater working: `brew update` runs `git fetch --force` + `git
rebase origin/main` per tap, and the old throwaway-snapshot design minted an
unrelated PARENTLESS root per build, wedging every client mid-rebase in add/add
conflicts after every publish (already-wedged clones recover once with `brew
update-reset`). Append-only objects also fix the dumb-HTTP consistency race (a
publish mid-`brew update` can no longer orphan the refs a client already fetched).

The in-memory layer (`tapcache.go`) is now just a rebuild-rate gate + open
`os.Root` cache: at most one content re-check per `tapCacheTTL` (~30s) per
lineage, expired entries swept on access, map capped (`tapCacheMaxEntries`, closes
fds only -- history stays). The DISK store is capped too
(`tapHistoryMaxLineages`, evict-whole-lineage, LRU by dir mtime) so junk Host
headers / deleted tokens can't grow it unboundedly; `resetTapCache` (OnReady)
deliberately does NOT remove the history root -- it sweeps only crash orphans
(temp files) and the legacy `{TmpDir}/brew-tap` snapshot root. Cost: ~2-4 new
small objects per content-changing publish per lineage.

## Smart-HTTP serving

`smart.go`: every tap root additionally answers the git smart protocol -- `GET
.../info/refs?service=git-upload-pack` (the ref advertisement, derived from the
SAME persisted lineage tip the dumb path serves; without the service param the
literal route falls through to the exact dumb file serving, and each root keeps
its credential semantics: the smart pair is served DIRECTLY on both
git.{domain}/brew/tap.git and brew.{domain}/tap.git -- both are first-class clone
URLs; the pair always lives UNDER a tap path, never at a host root, whose
namespace belongs to formula/project routes -- while /private/tap.git keeps its
401 challenge and only the bare /tap.git path + dumb file paths keep the anonymous
redirect) and `POST .../git-upload-pack` (the fetch: a version-2 packfile
assembled on the fly by walking parent/tree links through the lineage's loose
objects -- FULL history for a plain clone, so later dumb or smart fetches
fast-forward; depth-bounded with shallow/unshallow lines only when the client sent
deepen, since a plain clone's capability echo mistaken for a depth request used to
kill git with "expected ACK/NAK, got 'shallow'").

Negotiation is stateless-minimal: haves are never ACKed (each flush-terminated
batch gets NAK until the client sends done -- never NAK+pack, which would corrupt
the next round once history makes git batch its haves) and the final pack is
self-contained from the client's WANT -- the sha the advertisement handed it -- so
a publish landing mid-clone can never produce a ref/pack mismatch (append-only
store + the want pins the tip); while a smart request streams, its lineage dir is
pinned against the disk-cap eviction (`acquireTapLineage`, refcounts in
`Handler.tapPins`). Real git prefers smart automatically, so `brew tap`/`brew
update` now transfer one pack instead of ~4 loose GETs per past publish; dumb
clients (no service param) are byte-for-byte unchanged.

`ServeFormula` (single project) is uncached -- cheap once the digests are.
Self-registering via auth.OnReady().
