# Project names

Extracted verbatim from CLAUDE.md, no wording changed.

Project names are slash-namespaced and may nest to any depth (`<repo>/<binary>`, even deeper). The api-layer validator (`internal/api/projects.go`, generated regex) allows `/`-separated segments. The `wow-look-at-my/router` `{project}` token matches multiple path segments greedily (anchored by trailing literals like `releases`/`artifacts`), so no `%2F` encoding is needed. Storage is content-addressed so a `/` in a name never touches a filesystem path.

The deb/APT format is the exception that cannot carry a `/` in its identifier: its package-name grammar forbids `/` and `_`, so the deb package.

Homebrew folds the namespace too, but only in the tap FILENAME (`tapFormulaName`: `gcc/pgo` -> `gcc-pgo.rb`) -- see `docs/formats/brew-tap.md` for how the formula routes resolve a folded name back.

An OIDC-provisioned repo's token is authorized for its own project and any `<repo>/<...>` sub-namespace beneath it, gated by a trailing-slash boundary so sibling prefixes (`R-evil`).
