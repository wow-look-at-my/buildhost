# Why the admin dashboard shipped a bundle nothing built

## What happened

`internal/admin/static/app.js` was committed to git. It was also, at one point,
the output of a build. Those two facts together are the whole incident.

1. The admin frontend was hand-written JavaScript in `internal/admin/static/`,
   committed and embedded via `go:embed`. Fine: it was source.
2. A later change converted the frontend to TypeScript under
   `internal/admin/frontend/src/`, with esbuild producing `static/*.js`. The
   built files stayed committed, justified in CLAUDE.md as "so `go-toolchain`
   works without Node.js".
3. The conversion was **lossy** — the TypeScript never implemented the
   Retention page, token create/edit/delete, private-project download links,
   several cards, and it broke every service URL and the router's handling of
   slash-namespaced projects.
4. None of that was visible, because the *committed* `app.js` was still the old,
   complete bundle. It is what `go:embed` shipped, what the tests read, and what
   users got. The TypeScript was dead source that nothing executed.
5. Removing the committed artifacts (correctly — they are build output) made the
   binary embed a dashboard with no JavaScript, and turned two tests red on
   `open static/app.js: no such file`.

The divergence existed from the moment of the conversion. It surfaced only when
someone deleted the file that was hiding it.

## Why nothing caught it

- **`go:embed` cannot tell source from artifact.** It embedded whatever was on
  disk. On a fresh clone that was the committed bundle, so the build was green.
- **The tests read the artifact, not the source.** `static_test.go` asserted
  against `static/app.js`, so it was testing the very file that had stopped
  being generated. A test that reads a committed build output can only ever
  confirm the output — never that the source still produces it.
- **Nothing ever rebuilt in CI.** The build ran `go-toolchain`; the frontend
  build was a manual step in CLAUDE.md ("rebuild before running go-toolchain").
  A step that only a human remembers is a step that does not run.
- **The justification was self-sealing.** "Checked in so the build works without
  Node" is true, and it is exactly what removes the pressure that would have
  caught the drift. The cost is invisible until the day the artifact is deleted.

## The rule

**A file that a build produces is not a file that git tracks.** Every generated
input in this repo is gitignored and produced by a `//go:generate` directive, so
one command materializes all of them (see the Build section of `CLAUDE.md`). The
admin bundle is now one of them, via `scripts/build-admin-frontend.sh`.

Committing an artifact "for convenience" trades a build dependency for a
correctness hazard, and the hazard is silent: the artifact keeps working while
its source rots, and every test that reads it keeps passing.

## The check

Prose does not survive. `.github/workflows/ci.yml` runs every generate directive
and then makes two assertions, because the bug has two shapes and one check
catches only one of them:

```
git diff --quiet                      # 1. drift
git ls-files -i -c --exclude-standard # 2. committed at all
```

1. **Drift** — a committed artifact whose source no longer produces it.
   Regenerating rewrites the file, so the working tree is dirty. This is the
   assertion that would have gone red the first time the TypeScript and the
   committed bundle disagreed, instead of months later when the file was
   deleted.
2. **Committed at all** — a tracked file that `.gitignore` says is build output.
   Needed because a deterministic build (esbuild is one) regenerates a
   *byte-identical* artifact, leaving check 1 green while the artifact sits in
   git waiting to drift. This one flags it immediately, in sync or not.

## When a test must assert on built output

Assert on markup or behavior the build *emits*, not on identifiers the source
happens to contain. `static_test.go` originally checked for the strings
`App.projectTreeRows` and `App.projectLabel`; a bundler keeps those
module-scoped, so the assertion pinned an implementation detail while passing on
a build that rendered a flat list. It now asserts the CSS classes the tree
actually renders, which a regression cannot satisfy.
