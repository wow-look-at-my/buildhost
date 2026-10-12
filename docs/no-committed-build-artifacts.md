# Why the admin dashboard shipped a bundle nothing built

## What happened

`internal/admin/static/app.js` was committed to git. It was also, at one point, the output of a build. Those two facts together are the whole incident.

1. The admin frontend was hand-written JavaScript in `internal/admin/static/`, committed and embedded via `go:embed`. Fine: it was source.
2. A later change converted the frontend to TypeScript under `internal/admin/frontend/src/`, with esbuild producing `static/*.js`. The built files stayed committed, justified in CLAUDE.md as "so `go-toolchain` works without Node.js".
3. The conversion was **lossy**. The TypeScript never implemented the Retention page, token create, edit and delete, the private-project download links, or several cards. It also broke every service URL and the router's handling of a slash-namespaced project.
4. None of that was visible, because the *committed* `app.js` was still the old, complete bundle. It is what `go:embed` shipped, what the tests read, and what users got. The TypeScript was dead source that nothing executed.
5. Removing the committed artifacts (correctly — they are build output) made the binary embed a dashboard with no JavaScript, and turned two tests red on `open static/app.js: no such file`.

The divergence existed from the moment of the conversion. It surfaced only when someone deleted the file that was hiding it.

## Why nothing caught it

- **`go:embed` cannot tell source from artifact.** It embedded whatever was on disk. On a fresh clone that was the committed bundle. The build was therefore green.
- **The tests read the artifact, not the source.** `static_test.go` asserted against `static/app.js`. It was therefore testing the very file that had stopped being generated. A test that reads a committed build output can only ever confirm the output. It never confirms that the source still produces it.
- **Nothing ever rebuilt in CI.** The build ran `go-toolchain`. The frontend build was a manual step in CLAUDE.md: "rebuild before running go-toolchain". A step that only a human remembers is a step that does not run.
- **The justification was self-sealing.** "Checked in so the build works without Node" is true. It is also exactly what removes the pressure that catches the drift. The cost is invisible until the day the artifact is deleted.

## The rule

**A file that a build produces is not a file that git tracks.** Every generated input in this repo is gitignored and produced by a `//go:generate` directive, so one command materializes all of them (see the Build section of `CLAUDE.md`). The admin bundle is now one of them, via `scripts/build-admin-frontend.sh`.

To commit an artifact "for convenience" trades a build dependency for a correctness hazard. The hazard is silent. The artifact keeps working while its source rots, and every test that reads it keeps passing.

## The check

Prose does not survive. `.github/workflows/ci.yml` runs every generate directive and then makes two assertions, because the bug has two shapes and one check catches only one of them:

```
git diff --quiet                      # 1. drift
git ls-files -i -c --exclude-standard # 2. committed at all
```

1. **Drift** — a committed artifact whose source no longer produces it. A regenerate rewrites the file, so the working tree is dirty. This assertion goes red the first time the TypeScript and the committed bundle disagree. The old arrangement went red months later, when the file was deleted.
2. **Committed at all** — a tracked file that `.gitignore` says is build output. Needed because a deterministic build (esbuild is one) regenerates a *byte-identical* artifact, leaving check 1 green while the artifact sits in git waiting to drift. This one flags it immediately, in sync or not.

## How the restored bundle was verified

Grepping the two bundles for shared strings proves nothing much — a bundler splits string concatenation differently, so identical output reads as hundreds of differences. What settles it is rendering.

Both bundles were loaded into a `node:vm` sandbox with a stub DOM, driven through every route, and the resulting `#content` HTML compared byte-for-byte:

- with no backend, so each bundle used its own built-in demo payloads.
- with a stubbed `fetch` that served the old bundle's demo payloads to *both*, so they saw identical server responses.
- with the release page fixtured at `is_private` both false and true. That page's public and private split, plain links against mint-on-click signed links, is where the divergence was widest.

All thirteen surfaces — twelve routes plus the sidebar — came back identical in every pass. That is what "no functionality lost" was checked against, rather than asserted.

## The other direction: work that was TypeScript-only

The bug cuts both ways. The committed bundle is what shipped, so anything written in `frontend/src` after the conversion never ran either. A verbatim restore of the old behavior throws that away. A diff of the pristine TypeScript against the old bundle found exactly two such changes. Both are about URL handling, and both are kept.

- **`encodeURIComponent` on an admin API path.** A running server answers `200` to both `GET /api/projects/repo%2Fbinary` and `GET /api/projects/repo/binary` for a slash-namespaced project. That was verified. The change is therefore safe for the namespace feature, not merely untested.
- **A decode of the route segments.** Kept, and guarded by `decodeSegment`. A malformed `%` sequence falls back to the raw text instead of a throw. The old router cannot do that, because it never decoded at all.

Everything else in the TypeScript was a strict subset of the old bundle. `copy.js` builds byte-identical. The types are a superset. Every rendering difference was a loss, never an addition.

## When a test must assert on built output

Assert on markup or behavior the build *emits*. Do not assert on an identifier the source happens to contain. `static_test.go` originally checked for the strings `App.projectTreeRows` and `App.projectLabel`. A bundler keeps those module-scoped. The assertion therefore pinned an implementation detail, and it passed on a build that rendered a flat list. It now asserts the CSS classes the tree actually renders, which a regression cannot satisfy.
