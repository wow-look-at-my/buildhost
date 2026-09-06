# Homebrew formulas and the generated tap

`internal/brew/`. Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed.

## Routes

`brew.{domain}/tap.git` is a first-class cloneable tap URL. Its smart-HTTP pair is served directly. The bare `/tap.git` path and the dumb file paths still 301 an ANONYMOUS request to `git.{domain}/brew/tap.git`, which is equally cloneable.

A formula is also fetchable at `brew.{domain}/Formula/{project}.rb`, and at the legacy `brew.{domain}/{project}`. A tap formula FILENAME folds the slash namespace, so `tapFormulaName` turns `gcc/pgo` into `gcc-pgo.rb`. The formula routes therefore resolve a folded name back to its project when no project matches it literally. `Handler.parseRoute` does that.

A literally named project always wins. A fold candidate is restricted to a project the request may read, by `auth.TokenCanReadProject`, which is the tap-membership rule. An anonymous probe of a private project's folded name therefore stays a 404, and never a 401 existence leak. `requireProject` still applies its normal auth to the resolved project.

The `{project}.rb` pattern is ONE path segment. A slash-namespaced name therefore also gets `brew.{domain}/Formula/{path...}`. Without that route, `/Formula/gcc/pgo.rb` fell through to the legacy `/{project}` route. That route read the whole path as a project name, and answered "project not found".

The name a user TYPES is the folded one, as in `brew install pazer/build/gcc-pgo`. A Homebrew formula name cannot contain `/`. `repackage.BrewFormulaName` is that fold. The admin dashboard, the web frontend, llms.txt and the README all install through it.

## Authenticated tap

`brew.{domain}/private/tap.git` challenges an anonymous request with a 401 and a Basic realm. git does NOT send a URL-embedded credential preemptively, and waits for a challenge. A 200 therefore makes a credentialed `brew tap x:TOKEN@...` silently ingest the public-only tap.

The route then serves a tap scoped to the credential. That tap holds every public project, plus the private projects the token can read. `auth.TokenCanReadProject` decides that, which is the same rule requireProject applies to a single-project read. Tap membership can therefore never leak a name the token cannot read directly.

A credentialed request to plain `/tap.git` is likewise served in place, rather than redirected. A redirect drops the credential mid-flight.

A private formula calls `require_relative` on the tap's `lib/buildhost_private_download.rb` (`repackage.BrewPrivateStrategy`). It downloads `using: BuildhostCurlDownloadStrategy`. That strategy sends `Authorization: Bearer $HOMEBREW_BUILDHOST_TOKEN`. The `HOMEBREW_` prefix is mandatory, because Homebrew scrubs every other env var before formula code runs. The tap itself never embeds a token. The authenticated dl redirect completes the chain with its signed-token Location.

A credentialed tap or formula response carries `Cache-Control: private, no-store` and `Vary: Authorization`. The CDN can therefore never serve one scope's tap to another.

## Formula codegen must always emit valid, loadable Ruby

The class name comes from `repackage.BrewClassName`. It mirrors Homebrew's own filename-to-class derivation in `Formulary.class_s`. There `-`, `_` and `.` separate, and buildhost's folded `/` separates too. The next character is upcased, so `go1.2.3` becomes `Go123`.

A digit-leading project name is excluded from brew entirely, by `repackage.BrewEligibleProjectName`. A formula endpoint answers 404 for it, and the tap skips it. `class 7zip < Formula` is a Ruby syntax error. No substitute class satisfies brew's loader either, because `Formulary.class_s("7zip")` returns `"7zip"`, which is not a legal constant. Both facts were measured against Homebrew 6.0.9.

**Every formula carries a TOP-LEVEL `url` and `sha256`.** That is the canonical resource, which is linux/intel when present, and otherwise the first in stable (OS, Arch) order. `repackage.brewCanonicalResource` picks it. The per-platform `on_<os>` and `on_<arch>` stanzas come in addition.

Homebrew must find a stable URL on EVERY platform to import a formula at all. It otherwise reports "formula requires at least a URL". One failed import poisons whole-tap evaluation, so a linux-only project used to break the tap for every macOS user.

An `on_*` block still overrides `url` and `sha256` on a platform it matches. A formula whose resources span only ONE os additionally declares `depends_on :linux` or `depends_on :macos`. That is homebrew-core's single-platform pattern. A foreign-platform install therefore fails cleanly, instead of a fetch of a binary that cannot run.

## Opt-in `service do` block

`projects.create_service` arrives in migration 013. It is the packaging-agnostic background-service setting. The publishing repo's CI DECLARES it, as an optional `create_service` bool on release-create. The `buildhost-create-release` and `buildhost-publish` actions carry that input. It is asserted idempotently on EVERY publish. An absent value leaves the stored setting untouched, so an old CI never clobbers it. That follows the default_branch reassertion precedent. An operator can also flip it directly, through `PATCH /api/v1/projects/{project}`.

Only a BOOL crosses the wire either way. No publisher-controlled Ruby can therefore enter the template.

A flagged binary-kind project's formula gains a `service do` block. It carries `run [opt_bin/"<InstallName>"]`, and the opt path survives an upgrade. It carries `keep_alive successful_exit: false`, which is launchd's `KeepAlive {SuccessfulExit: false}`, and means CRASH-ONLY restart. A plain `keep_alive true` respawns a deliberately-exiting app about every 10 seconds, such as a single-instance exit-0 handoff. It carries `log_path` and `error_log_path` under `var/"log/"`, and brew services mkpaths the log parents itself before load, through `Service#path_dirs`. It carries `process_type :interactive`.

ONE `brew services start <tap>/<project>` then manages the binary as a login service. On macOS that is a user LaunchAgent in the gui domain. An upgrade keeps it running, through opt_bin.

Install-time auto-start is STRUCTURALLY impossible in a formula. `post_install` is the only install hook. It runs inside brew's seatbelt sandbox. That profile is a global `(deny file-write*)` plus a build-path allowlist, from `formula_installer.rb` post_install and `extend/os/mac/sandbox.rb` SEATBELT_ERB. `~/Library/LaunchAgents` is therefore unwritable. A nested `brew services start` cannot work there. A failing `post_install` also sets `Homebrew.failed`, which is a red install. Never ship one.

`brew uninstall` does NOT stop a service, because only `caveats.rb` consumes `Utils::Service`. Document `brew services stop` before removal.

On Linux, brew's systemd user units carry no graphical-session ordering and no environment. That is documented as a poor fit for a GUI app. The deb materialization is the Linux path. See `internal/repackage`.

A non-binary kind never emits the block, because the block references `opt_bin`, which only `bin.install` stages. The flag-off rendering is pinned BYTE-IDENTICAL to the pre-flag output. Formula bytes feed the tap's content-addressed git objects, so off-state drift mints a spurious tap commit for every project.

A slash-named project calls `bin.install` on the BASENAME. The tar.gz's only top-level entry is the namespace dir, and brew strips a lone top-level dir when it unpacks. The staged file is therefore just the basename. An install of the slashed path answers ENOENT.

## Installed file modes

**A binary-kind formula pairs `chmod 0755, bin/"<InstallName>"` with `skip_clean "bin"`.** Homebrew's Cleaner rewrites the mode of everything under `bin`, whatever the formula installed. `Library/Homebrew/extend/os/{linux,mac}/cleaner.rb` holds it. It writes `0555` for a file it recognizes as executable, which is a `#!` script, an ELF, or a Mach-O. It writes `0444` for anything else.

A Cosmopolitan APE binary such as go-toolchain was therefore installed `0444`. Nothing was able to execute it. `0555` is no better. An APE assimilates itself on first run, which rewrites its own file into a native ELF or Mach-O. Without the write bit it dies with "cannot create <path>: Permission denied".

`skip_clean` prunes the Cleaner for `bin`, through `Formula#skip_clean?` and `Find.prune`. The installed `0755` therefore survives. The mode the tar.gz ships, which is `0755` for `kind=binary`, is irrelevant to brew either way. A non-binary kind stages nothing under `bin` and keeps the default cleanup. This was verified against Homebrew 6.x on Linux, end to end: `0444` before, and `0755` plus a successful self-assimilating run after.

## Digest cache

A formula download URL points at a tar.gz artifact. Each sha256 is computed from the same tar.gz payload Homebrew downloads. Each one is **cached in `packaged_artifacts` under `format="tar.gz"`**. `tarGZSHA256` in `formula.go` does that. It fills lazily on the first request. Every later request is a DB read, rather than a full repackage and hash per artifact.

The row is a digest cache only. `storage_key` records the SOURCE artifact blob. No tar.gz is stored, and a download still repackages on demand.

That is sound because tar.gz generation is deterministic per artifact. The tar header's name, size and mode are fixed, and every mtime is zero. The gzip header is fixed. The input is content-addressed. `TestTarGZGenerationDeterministic` pins it. The rows ride the existing retention cascade, and `deleteReleaseRows` drops them with their artifacts.

## Tap git history

The tap itself is served from a **persistent, append-only, per-lineage git history**. It lives under `{DataDir}/brew-tap/<sha256(key)>/`, in `taphistory.go`. It is NOT under the swept `{DataDir}/tmp`. That follows the durable-state precedent of `apt-signing.key` and `download-signing.key`.

There is one lineage per **(request-derived base URL, credential scope)** key. The base URL is part of the key because a tap must never be served with another host's URLs baked in. The credential scope is part of it because tap contents depend on what the credential may read. `tapScopeKey` computes that scope, as anon, a DB-token ID, or an OIDC subject plus namespace. One scope's lineage can therefore never be handed to another.

Each lineage is a bare dumb-HTTP layout, with loose `objects/`, `refs/heads/main`, `info/refs` and `HEAD`. It is served by mmap through an `os.Root`. That is the storage-layer pattern, with no heap buffering and no path escape.

**A ref only ever fast-forwards.** A rebuild reads the persisted tip. It REUSES that tip when the new content's tree is unchanged, so a periodic rebuild adds no growth. The commit sha is deterministic, from zero timestamps, a fixed identity, and content. A rebuild otherwise appends a commit with `parent <tip>`.

Objects are written BEFORE the ref advances, content-addressed, by temp file and rename. A reader therefore never sees a ref that names a missing object. A crash leaves a consistent store.

This is what keeps Homebrew's updater working. `brew update` runs `git fetch --force` and `git rebase origin/main` per tap. The old throwaway-snapshot design minted an unrelated PARENTLESS root per build. That wedged every client mid-rebase, in add/add conflicts, after every publish. An already-wedged clone recovers once, with `brew update-reset`. Append-only objects also fix the dumb-HTTP consistency race. A publish mid-`brew update` can no longer orphan a ref a client already fetched.

The in-memory layer, in `tapcache.go`, is now a rebuild-rate gate plus an open `os.Root` cache. It allows at most one content re-check per `tapCacheTTL`, about 30 seconds, per lineage. It sweeps an expired entry on access. The map is capped by `tapCacheMaxEntries`, and eviction closes file descriptors only, so the history stays.

The DISK store is capped too, by `tapHistoryMaxLineages`. It evicts a whole lineage, LRU by directory mtime. A junk Host header or a deleted token is therefore unable to grow it without bound. `resetTapCache`, which runs from OnReady, deliberately does NOT remove the history root. It sweeps only a crash orphan, meaning a temp file, and the legacy `{TmpDir}/brew-tap` snapshot root. The cost is a handful of new small objects per content-changing publish per lineage.

## Smart-HTTP serving

`smart.go` makes every tap root additionally answer the git smart protocol.

`GET .../info/refs?service=git-upload-pack` is the ref advertisement. It is derived from the SAME persisted lineage tip the dumb path serves. Without the service parameter the literal route falls through to the exact dumb file serving.

Each root keeps its credential semantics. The smart pair is served DIRECTLY on `git.{domain}/brew/tap.git` and on `brew.{domain}/tap.git`, and both are first-class clone URLs. The pair always lives UNDER a tap path, and never at a host root, whose namespace belongs to the formula and project routes. `/private/tap.git` keeps its 401 challenge. Only the bare `/tap.git` path and the dumb file paths keep the anonymous redirect.

`POST .../git-upload-pack` is the fetch. It assembles a version-2 packfile on the fly, by a walk of the parent and tree links through the lineage's loose objects. A plain clone gets FULL history, so a later dumb or smart fetch fast-forwards. The pack is depth-bounded, with shallow and unshallow lines, only when the client sent `deepen`. A plain clone's capability echo was once mistaken for a depth request, which killed git with "expected ACK/NAK, got 'shallow'".

Negotiation is stateless-minimal. A have is never ACKed. Each flush-terminated batch gets NAK until the client sends done. It never gets NAK plus a pack, which corrupts the next round once history makes git batch its haves.

The final pack is self-contained from the client's WANT, which is the sha the advertisement handed it. A publish that lands mid-clone can therefore never produce a ref and pack mismatch. The append-only store and the want together pin the tip. While a smart request streams, its lineage directory is pinned against the disk-cap eviction. `acquireTapLineage` does that, and `Handler.tapPins` holds the refcounts.

Real git prefers smart automatically. `brew tap` and `brew update` now transfer one pack, instead of several loose GETs per past publish. A dumb client, which sends no service parameter, is byte-for-byte unchanged.

`ServeFormula` serves a single project, and it is uncached. It is cheap once the digests are cached. The package self-registers through init().
