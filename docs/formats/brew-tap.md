# Homebrew formulas and the generated tap

`internal/brew/`. Extracted verbatim from CLAUDE.md. Paragraph breaks were added at the existing topic boundaries, no wording changed.

## Routes

`brew.{domain}/tap.git` is a first-class cloneable tap URL (its smart-HTTP pair is served directly. The bare `/tap.git` path and the dumb file paths still 301 ANONYMOUS requests to the equally cloneable `git.{domain}/brew/tap.git`). Formulas are also fetchable at `brew.{domain}/Formula/{project}.rb` and the legacy `brew.{domain}/{project}` -- and because tap formula FILENAMES fold the slash namespace (`tapFormulaName`: `gcc/pgo` -> `gcc-pgo.rb`), the formula routes resolve. A literally named project always wins, fold candidates are restricted to projects the request may read -- `auth.TokenCanReadProject`, the tap-membership rule -- so an anonymous probe. The `{project}.rb` pattern is ONE path segment, so a slash-namespaced name also gets `brew.{domain}/Formula/{path...}`: without it `/Formula/gcc/pgo.rb` fell through to the legacy `/{project}` route.

The name a user TYPES is the folded one -- `brew install pazer/build/gcc-pgo` -- because a Homebrew formula name cannot contain `/`. `repackage.BrewFormulaName` is that fold, and the admin dashboard, the web frontend, llms.txt and the README all install through it.

## Authenticated tap

`brew.{domain}/private/tap.git`: challenges anonymous requests with a 401 + Basic (git does NOT send URL-embedded credentials preemptively -- it waits for a challenge -- so a 200 will make. A credentialed request to plain `/tap.git` is likewise served in place instead of redirected (a redirect will drop the credential mid-flight). Private formulas `require_relative` the tap's `lib/buildhost_private_download.rb` (`repackage.BrewPrivateStrategy`) and download `using: BuildhostCurlDownloadStrategy`, which sends `Authorization: Bearer $HOMEBREW_BUILDHOST_TOKEN` (HOMEBREW_-prefixed because Homebrew scrubs all other env vars before formula code runs). The tap itself never embeds a token, and the authenticated dl redirect completes the chain with its signed-token Location. Credentialed tap/formula responses are `Cache-Control: private, no-store` + `Vary: Authorization` so the CDN can never serve one scope's tap to another.

## Formula codegen must always emit valid, loadable Ruby

The class name is `repackage.BrewClassName`, which mirrors Homebrew's own filename->class derivation (`Formulary.class_s`: `-`, `_`, `.` -- and buildhost's folded `/` -- separate, next char upcased. `go1.2.3` -> `Go123`), and a digit-leading project name is excluded from brew entirely via `repackage.BrewEligibleProjectName` (formula endpoints 404, tap skips it) because `class 7zip < Formula` is a Ruby syntax error and no substitute. Both measured against Homebrew 6.0.9).

**Every formula carries a TOP-LEVEL `url`+`sha256`** (the canonical resource -- linux/intel when present, else first in stable (OS, Arch) order. `repackage.brewCanonicalResource`) in addition to the per-platform `on_<os>`/`on_<arch>` stanzas, because Homebrew must find a stable URL on EVERY platform to import a formula at all. The on_* blocks still override url/sha256 on platforms they match, and a formula whose resources span only ONE os additionally declares `depends_on :linux`/`:macos` (homebrew-core's single-platform pattern).

## Opt-in `service do` block

`projects.create_service`, migration 013 -- the packaging-agnostic background-service setting, DECLARED by the publishing repo's CI as an optional `create_service` bool on release-create (the `buildhost-create-release`/`buildhost-publish` actions' `create_service`. Asserted idempotently on EVERY publish, absent = stored setting untouched so an old CI never clobbers it -- the default_branch reassertion precedent), or flipped directly by. Either way only a BOOL crosses the wire, so no publisher-controlled Ruby can enter the template: a flagged binary-kind project's formula gains a `service do` block. Upgrades keep it running via opt_bin).

Install-time auto-start is STRUCTURALLY impossible in a formula -- post_install (the only install hook) runs inside brew's seatbelt sandbox whose profile is a global `(deny file-write*)` plus a build-path allowlist. A failing post_install also sets Homebrew.failed (red install) -- never ship one. `brew uninstall` does NOT stop services (Utils::Service is consumed only by caveats.rb): document `brew services stop` before removal. On Linux brew's systemd user units carry no graphical-session ordering/env, documented as a poor GUI-app fit (the deb materialization is the Linux path -- see internal/repackage). Non-binary kinds never emit the block (it references `opt_bin`, which only `bin.install` stages), and the flag-off rendering is pinned BYTE-IDENTICAL to the pre-flag output (formula bytes feed the tap's content-addressed git objects -- off-state drift will mint a spurious tap commit for every project).

Slash-named projects `bin.install` the BASENAME -- the tar.gz's only top-level entry is the namespace dir and brew strips a lone top-level dir when unpacking. So the staged file is just the basename. Installing the slashed path ENOENTs.

## Installed file modes

**A binary-kind formula pairs `chmod 0755, bin/"<InstallName>"` with `skip_clean "bin"`**: Homebrew's Cleaner rewrites the mode of everything under `bin` regardless of what the formula installed (`Library/Homebrew/extend/os/{linux,mac}/cleaner.rb` -- `0555`. `0444` for anything else), so a Cosmopolitan/APE binary such as go-toolchain was installed `0444` and can not be executed at all -- and `0555` is no. `skip_clean` prunes the Cleaner for `bin` (`Formula#skip_clean?` -> `Find.prune`), so the installed `0755` survives. The mode the tar.gz ships (`0755` for `kind=binary`) is irrelevant to brew either way. Non-binary kinds stage nothing under `bin` and keep the default cleanup. Verified against Homebrew 6.x on Linux end to end: `0444` before, `0755` + a successful self-assimilating run after.

## Digest cache

Formula download URLs point to tar.gz artifacts and sha256 values are computed from the same tar.gz payload Homebrew downloads -- and are **cached in `packaged_artifacts` under. The row is a digest cache only -- `storage_key` records the SOURCE artifact blob, no tar.gz is stored, downloads still repackage on demand -- which is sound. Pinned by `TestTarGZGenerationDeterministic`), and the rows ride the existing retention cascade (`deleteReleaseRows` drops them with their artifacts).

## Tap git history

The tap itself is served from a **persistent, append-only, per-lineage git history** under `{DataDir}/brew-tap/<sha256(key)>/` (`taphistory.go`. NOT under the swept `{DataDir}/tmp` -- same durable-state precedent as `apt-signing.key`/`download-signing.key`), one lineage per **(request-derived base URL, credential scope)** key -- the base URL because a tap must never be. Each lineage is a bare dumb-HTTP layout (loose `objects/`, `refs/heads/main`, `info/refs`, `HEAD`) served by mmap through an `os.Root` (the storage-layer pattern. No heap buffering, no path escape).

**Refs only ever fast-forward**: a rebuild reads the persisted tip, REUSES it when the new content's tree is unchanged (no growth from periodic rebuilds. This is what keeps Homebrew's updater working: `brew update` runs `git fetch --force` + `git rebase origin/main` per tap, and the old throwaway-snapshot design minted an unrelated PARENTLESS root per build. Append-only objects also fix the dumb-HTTP consistency race (a publish mid-`brew update` can no longer orphan the refs a client already fetched).

The in-memory layer (`tapcache.go`) is now just a rebuild-rate gate + open `os.Root` cache: at most one content re-check per `tapCacheTTL` (~30s) per lineage, expired entries. The DISK store is capped too (`tapHistoryMaxLineages`, evict-whole-lineage, LRU by dir mtime) so junk Host headers / deleted tokens cannot grow it unboundedly. `resetTapCache` (OnReady) deliberately does NOT remove the history root -- it sweeps only crash orphans (temp files) and the legacy `{TmpDir}/brew-tap` snapshot root. Cost: ~2-4 new small objects per content-changing publish per lineage.

## Smart-HTTP serving

`smart.go`: every tap root additionally answers the git smart protocol -- `GET .../info/refs?service=git-upload-pack` (the ref advertisement, derived from the SAME persisted lineage tip the dumb path serves. Without the service param the literal route falls through to the exact dumb file serving, and each root keeps its credential semantics: the smart pair. The pair always lives UNDER a tap path, never at a host root, whose namespace belongs to formula/project routes -- while /private/tap.git keeps its 401 challenge. Depth-bounded with shallow/unshallow lines only when the client sent deepen, since a plain clone's capability echo mistaken for a depth request used to kill git.

Negotiation is stateless-minimal: haves are never ACKed (each flush-terminated batch gets NAK until the client sends done -- never NAK+pack, which will corrupt the next round. While a smart request streams, its lineage dir is pinned against the disk-cap eviction (`acquireTapLineage`, refcounts in `Handler.tapPins`). Real git prefers smart automatically, so `brew tap`/`brew update` now transfer one pack instead of ~4 loose GETs per past publish. Dumb clients (no service param) are byte-for-byte unchanged.

`ServeFormula` (single project) is uncached -- cheap once the digests are. Self-registering via init().
