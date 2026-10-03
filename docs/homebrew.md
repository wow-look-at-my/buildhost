# Homebrew

buildhost exposes a generated Homebrew tap as a Git repository. Add the tap once, trust it, then install formulas through the tap name. `brew trust` is required since Homebrew 6.0, which refuses to evaluate third-party taps until they are trusted (older brews have no `trust` command and enforce nothing):

```bash
brew trust https://brew.pazer.build/tap.git
brew tap pazer/build https://brew.pazer.build/tap.git
brew install pazer/build/go-toolchain
```

Do not install a formula with a naked remote URL, such as `brew install https://brew.pazer.build/go-toolchain`. Modern Homebrew reads that as a formula name or a tap name. It does not clone it as a formula URL.

On Linux these formulas have no bottle. `brew install` therefore runs Homebrew's build sandbox. That sandbox needs bubblewrap, from `apt install bubblewrap`, and Homebrew also instills its own. It needs an unprivileged user namespace too. A hardened host such as Ubuntu 24.04 may need `sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0`. In a container or a CI runner with no user namespace, set `HOMEBREW_NO_SANDBOX_LINUX=1` instead. macOS needs neither.

A slash-namespaced project folds `/` to `-` in its formula name. That is the same rule APT applies to a package name. Project `log-streamer/client` therefore installs as `brew install pazer/build/log-streamer-client`.

A project whose name starts with a digit cannot be served as a formula at all. Homebrew derives the Ruby class from the formula name, and a Ruby class cannot start with a digit. Such a project is therefore omitted from the tap.

## Private projects

A private project never appears in the public tap. Tap the **authenticated tap** instead. It serves every public formula, plus the private projects your token can read. It therefore replaces the public tap under the same name. Remove the public tap first, with `brew untap --force pazer/build`, when you already added it.

Git transmits a credential only after a 401 challenge. The token therefore goes in the tap URL, as the HTTP Basic password. The username is ignored, and `x` is the convention.

An artifact download authenticates separately, through `HOMEBREW_BUILDHOST_TOKEN`. A private formula reads that at install time. The token is never written into the tap.

The example below uses a private project named `myrepo/myapp`. Under the folding rule above it installs as `myrepo-myapp`. The installed command keeps the binary's own name, `myapp`:

```bash
brew trust "https://x:$TOKEN@brew.pazer.build/private/tap.git"
brew tap pazer/build "https://x:$TOKEN@brew.pazer.build/private/tap.git"
export HOMEBREW_BUILDHOST_TOKEN="$TOKEN"
brew install pazer/build/myrepo-myapp
```

`brew update` refreshes the tap with the credential stored in the tap's git remote. The `?token=` query parameter does not work with `brew tap`. git appends its own path segments after the query string, such as `/info/refs`. The URL then stops resolving as a git repository.

## Specific versions

The tap carries one versioned formula per published release on the project's default branch, named `<formula>@<version>`. The version is the release version without a leading `v`. Install one through the tap you already added:

```bash
brew install pazer/build/go-toolchain@1.0.0
```

A versioned formula is keg-only and never linked automatically, so it installs beside the unversioned formula without a link conflict, in either order. Run it from `$(brew --prefix pazer/build/go-toolchain@1.0.0)/bin/go-toolchain`. To put it on PATH instead, run `brew unlink go-toolchain` when the unversioned formula is installed, then `brew link --force go-toolchain@1.0.0`.

A private project's versions come through the authenticated tap, with `HOMEBREW_BUILDHOST_TOKEN` set as above. `myrepo/myapp` 0.9.0 installs as `brew install pazer/build/myrepo-myapp@0.9.0`.

A version that does not start with a digit has no versioned formula. Homebrew turns `@<digit>` into `AT` in the Ruby class name, and any other `@` leaves the class name invalid.

## Background services (create_service)

A project can declare that its binary runs as a background service. Declare it in the publishing repo's CI, with `create_service: 'true'` on the `buildhost-create-release` or `buildhost-publish` action. go-toolchain's composite spells that as `autorelease_args: create_service=true`. Every publish asserts the declared value. An absent input leaves the stored setting untouched. An operator can also flip it directly, with `PATCH /api/v1/projects/{project}` and a body of `{"create_service": true}`.

Each install format materializes the setting its own way. A Homebrew formula gains a `service do` block. One command activates it, once. It then starts at login, and it survives an upgrade, because the block runs the `opt` path.

```bash
brew services start pazer/build/competent-search-thing
```

Homebrew cannot run that for you at install. A formula's only install-time hook is `post_install`. That hook runs inside brew's sandbox. That profile denies every file write outside a build path, and `~/Library/LaunchAgents` is one of them. No formula can therefore register a LaunchAgent. `brew uninstall` does not stop a service either. Run `brew services stop <tap>/<project>` before you remove it.

The service restarts only after a crash, through `keep_alive successful_exit: false`. A clean exit stays exited. It logs to `$(brew --prefix)/var/log/<name>.log`. On Linux prefer the APT install below, because brew's Linux units carry no graphical-session ordering. The APT section describes the deb materialization, which does auto-enable. Every other format, meaning raw, zip, npm and OCI, stores the flag and materializes nothing.
