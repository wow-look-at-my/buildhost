# Visibility overrides

Who may read a build is decided at a few levels. Each level is `auto`, `public` or `private`. The most specific level that is not `auto` wins.

| Level | Key | Set in the admin dashboard |
| --- | --- | --- |
| Build | a release, or a site branch | Release page, project page (Releases and Sites tables) |
| Project | the project | Project page, Visibility row |
| Folder | a project-name prefix; covers `<prefix>` and `<prefix>/...` | Projects page, folder rows |
| Source | the GitHub repo visibility from the publish's OIDC token, or the creator's `is_private` | (not set by hand) |

`auto` at every level is the original behaviour: the repo that published the project decides.

## Storage

- `projects.is_private` is the EFFECTIVE project state. Every read path checks it, so a folder or project override needs no change in any handler. `db.applyProjectVisibility` writes it.
- `project_visibility.auto_private` is the source value. The OIDC write path calls `db.SyncAutoPrivate`, which updates this column and re-resolves. It never writes `is_private` directly, so an override survives the next publish.
- `visibility_folders`, `release_visibility` and `site_visibility` hold only non-auto rules. `auto` deletes the row.
- A folder change re-resolves every project it covers. A new project is resolved at creation. A folder rule applies from the first publish.
- Branch keys site_visibility`, so an override outlives each redeploy. With no override, a site published with `X-Public-Site: true` reads as public.

## Build enforcement

A read route that names one build implements `auth.BuildVisibilityRoute`. `requireProject` applies the build's mode over `project.IsPrivate` and stores the result for `auth.ReadIsPrivate`. Handlers use that value, not `project.IsPrivate`, for signed redirects and `Cache-Control`. A failed override lookup reads as private.

| Route | Build it resolves |
| --- | --- |
| `dl` | the release `v` / `branch` / latest resolves to |
| `static` | the release `v` names |
| npm tarball | the release in the tarball file name |
| OCI manifest by tag | the tag's release; PRIVATE overrides only |
| sites | the branch the read resolves to |

Limits, stated here and on the release page in the dashboard:

- Package indexes (APT `Packages`, Homebrew formulas and tap, the npm packument, the OCI tag list) follow the project. A private build of a public project is listed there. Its bytes still need a token, because every byte path above checks the build.
- A public build of a private project stays private to `docker pull`. Digest fetches image layers. A digest names no release.
- A response cached while public (`public, immutable` on `static`) can outlive a later switch to private in a CDN. Private builds are served `private, no-store`.
