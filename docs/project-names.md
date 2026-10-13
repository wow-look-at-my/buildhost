# Project names

Extracted verbatim from CLAUDE.md, no wording changed.

Project names are slash-namespaced. They nest to any depth (`<repo>/<binary>`, and deeper). The api-layer validator allows `/`-separated segments (`internal/api/projects.go`, a generated regex). The `wow-look-at-my/router` `{project}` token matches multiple path segments greedily, anchored by a trailing literal such as `releases` or `artifacts`. No `%2F` encoding is needed. Storage is content-addressed, so a `/` in a name never touches a filesystem path.

The deb/APT format is the exception that cannot carry a `/` in its identifier. Its package-name grammar forbids `/` and `_`. So `repackage.DebPackageName` folds both to `-` for the deb package name, and `pr-reviewer-agent/server` installs as `pr-reviewer-agent-server`. The slash stays in the repo URL.

Homebrew folds the namespace too, but only in the tap FILENAME (`tapFormulaName` turns `gcc/pgo` into `gcc-pgo.rb`). See `docs/formats/brew-tap.md` for how the formula routes resolve a folded name back to its project. That doc also covers the digit-leading names brew cannot represent at all.

An OIDC-provisioned repo's token is authorized for its own project and for any `<repo>/<...>` sub-namespace beneath it. A trailing-slash boundary gates that. A sibling prefix such as `R-evil` is refused, and so is an unrelated project. See `docs/security/oidc.md`.
