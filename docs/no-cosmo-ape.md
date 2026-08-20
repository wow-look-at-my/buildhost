# buildhost ships per-platform binaries, never a fat APE

`ci.yml` pins `os` and `arch` on the `go-toolchain` action. Without them the
action builds ONE Actually Portable Executable with the gosmopolitan fork
(`GOOS=cosmo`), and buildhost cannot compile that way.

## Why

The server embeds SQLite through `modernc.org/sqlite`, which sits on
`modernc.org/libc`. That library splits into about twenty subpackages -- `errno`,
`grp`, `limits`, `poll`, `pthread`, `pwd`, `signal`, `stdio`, `stdlib`,
`sys/types`, `time`, `unistd`, and more. Every file in them is named for its
target: `errno_linux_amd64.go`, `errno_darwin_arm64.go`, and so on. None is named
for cosmo.

Go's filename matching is literal about GOOS, and the cosmo fork does not change
that. The fork does add `cosmo` to `UnixOS`, so a `//go:build unix` file compiles
-- but a file named `_linux_amd64.go` still builds for linux only. So under
`GOOS=cosmo` every one of those subpackages resolves to zero Go files, and the
build stops with:

```
imports modernc.org/libc/errno: build constraints exclude all Go files in
gitlab.com/cznic/libc@v1.72.5/errno
```

repeated once per subpackage. This is a property of the dependency, not a
misconfiguration, and nothing on the buildhost side can satisfy it. A CGo SQLite
driver would not help either: the cross-compile matrix needs CGO off.

## What this costs, and what it does not

Nothing that shipped before. `os: linux,darwin,windows` and `arch: amd64,arm64`
are exactly the values the action defaulted to until it made the APE the default
(go-toolchain#366, 2026-08-16), so the published artifact set is unchanged. What
buildhost gives up is the single-binary-runs-everywhere form, which it never had.

## The failure this documents

buildhost's `master` last ran CI on 2026-08-15, one day before that default
changed. Nothing pushed to `master` afterwards, so the branch sat red without a
red run to show for it: the next push to any branch inherited the APE build and
failed on a diff that had nothing to do with it. Any repo whose dependencies are
GOOS-filename-keyed inherits the same break, and pinning the matrix is the fix.
