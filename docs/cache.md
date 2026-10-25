# CI cache

A cache entry is a directory a job of a GitHub Actions run saves under a key, for jobs of later runs to restore. It replaces `actions/cache`, whose repository quota evicts an entry while runs still need it. `buildhost-cache-save` packs a directory as a zstd-compressed tar with `buildhost archive`, as the hand-off does, and stores it. `buildhost-cache-restore` restores it. A run file (`docs/run-files.md`) is the other half: it carries a directory between the jobs of one run, and goes when the run ends.

```yaml
- uses: wow-look-at-my/buildhost/.github/actions/buildhost-cache-restore@lkgb
  with:
    path: pkg/obj/go-build
    key: dist-bootstrap-${{ runner.os }}-${{ github.sha }}
    restore-keys: dist-bootstrap-${{ runner.os }}-
- run: ./make.bash
- uses: wow-look-at-my/buildhost/.github/actions/buildhost-cache-save@lkgb
  with:
    path: pkg/obj/go-build
    key: dist-bootstrap-${{ runner.os }}-${{ github.sha }}
```

## Contract

- An entry belongs to a repository and a ref. Both come from the caller's verified OIDC token: the repository id, and the `ref` claim, such as `refs/heads/master` or `refs/pull/7/merge`.
- A restore reads the run's own ref, then the default branch. A pull request reads master's entries. It never reads another pull request's entries, and master never reads a pull request's entries. That is GitHub's own cache rule. It keeps a pull request from poisoning the cache master builds from.
- A restore tries the exact key in each scope first. Then it tries each restore key, in order, as a prefix, in each scope. A prefix takes the newest entry it matches.
- `cache-hit` is `true` only for an exact match. `cache-matched-key` names the entry restored. A miss leaves `path` as it is, unless `fail-on-cache-miss: true`.
- A key is written once in a ref. A save of a key the ref holds stores nothing and reports so. The server answers such a save with 409.
- buildhost reads the default branch from GitHub. When GitHub does not answer, it uses the branch the request names in `default_branch`. The actions take that from the run's event payload. A false name only widens what the repository's own run reads: the default branch searches its own ref first, which is that branch. With neither, a restore reads the run's own ref alone, and its answer and its log line say why.
- `path` names files and directories, one per line. A relative path is from the workspace. A leading `~` is the home directory. Name refuses a glob or a `!` exclusion. A save leaves out a path that does not exist, with a warning, and fails when none exists.
- An entry restores to the paths it was saved from. A restore names the same paths as its save. When every path is inside the workspace, the entry holds them relative to it, and a runner with another workspace directory restores them there. Otherwise the entry holds them from the filesystem root, and every path must be on one drive.

## Authentication

The job's OIDC token, minted with the server URL as its audience, with `permissions: id-token: write`. The request names its repository as `repository`. A request that names another repository than its token is refused with 403. A token with no `ref` claim names no scope and is refused.

## Endpoints

- Both reads take an optional `default_branch=`, used only when GitHub does not answer the default branch.
- `GET /api/v1/cache?repository=&key=&restore_keys=...` answers the entry a restore reads: `{"key", "ref", "size", "sha256", "exact", "scopes"}`. A miss is 404 with `scopes` and, when the default branch was not read, `default_branch`.
- `GET /api/v1/cache/{key}?repository=&ref=` serves an entry. `ref` must be one the caller's restore reads. It honors `Range` and carries `X-Content-SHA256`.
- `PUT /api/v1/cache/{key}?repository=` stores the body for the caller's ref, or a finished upload session through `upload_session`. It answers 201, or 409 when the ref holds the key.

A key is 1 to 512 of `[A-Za-z0-9._-]`.

## Eviction

`internal/cicache` runs a janitor every few minutes, on every server. It is not opt-in.

- It deletes an entry no restore used for `BUILDHOST_CACHE_KEEP`. The default is `168h`. A restore moves the entry's last use to now.
- While free disk on the data directory is below `BUILDHOST_CACHE_MIN_FREE_PERCENT` (default `15`), it deletes the least recently used entries until free disk is back above it. `0` turns this off.
- The cache floor sits above the run files floor (`BUILDHOST_RUN_FILES_MIN_FREE_PERCENT`, default `10`). A cache entry therefore goes before a hand-off that a live run still waits for.
- It waits while writes are in flight. It cannot delete a blob an upload stored again.

There is no per-repository quota. Disk is the only bound.

The admin dashboard's **CI Cache** page (`GET /api/cache` on the admin port) lists every entry: repository, ref, key, size, last use, restores, and when the janitor deletes it.

A malformed `BUILDHOST_CACHE_KEEP` or `BUILDHOST_CACHE_MIN_FREE_PERCENT` fails startup.
