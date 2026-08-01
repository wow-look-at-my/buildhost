# Admin dashboard frontend

The admin dashboard's browser code is TypeScript under
`internal/admin/frontend/src/`. The `internal/admin/static/*.js` it compiles to
are **gitignored build artifacts** produced by a `//go:generate` directive, like
the CA bundle and the sqlc/regex code -- there is no committed JavaScript, and
the TypeScript is the only source.

```
internal/admin/frontend/src/app.ts    # the whole dashboard (router, pages, API calls)
internal/admin/frontend/src/html.ts   # the escaping HTML builder app.ts renders through
internal/admin/frontend/src/copy.ts   # the <copy-btn> custom element
internal/admin/frontend/src/types.ts  # the admin API's response shapes
```

## Building

`go-toolchain --generate <hash>` materializes everything, including this. To
rebuild just the frontend (what the directive runs):

```bash
./scripts/build-admin-frontend.sh
```

Or from the frontend directory, `npm run build` (type-check + bundle) and
`npm run check` (`tsc --noEmit` alone). `npm ci` is only needed once; the script
does it for you when `node_modules` is absent.

npm is therefore required to build buildhost from a clean tree. That is the
deliberate trade: the alternative -- committing the output so a Node-less build
works -- is exactly what produced the drift described below.

## Why the embed names every file

```go
//go:embed static/index.html static/style.css static/app.js static/copy.js
```

A `static/*` wildcard still matches `index.html` and `style.css`, so a build
that skipped generate would compile clean and serve a dashboard whose every
script 404s -- a blank page, discovered by a user. Naming the generated files
makes the same mistake a compile error
(`pattern static/app.js: no matching files found`).

## The drift this layout exists to prevent

The dashboard was converted to TypeScript in #152, but the conversion never
took effect: it added a partial `src/app.ts` while the hand-written
`static/app.js` kept shipping, and both were committed. The two then diverged
for months -- the live bundle grew the Retention page, the project tree, token
CRUD, temp download links and the `/tap.git` fix; the TypeScript source had none
of them. The documented "edit the TS, rebuild, commit" workflow could not
reproduce the shipped artifact, and generating from that source would have
silently regressed the dashboard.

Two properties keep it from recurring, and both are load-bearing:

- **The output is generated, never committed.** There is no second copy to drift
  from. `internal/admin/static/*.js` is in `.gitignore`.
- **The tests read the generated bundle**, not the source
  (`internal/admin/static_test.go`). Chief among them,
  `TestAdminStaticInlineHandlersAreExported` cross-checks every
  `onclick="App.x(...)"` reference in the rendered markup against the bundle's
  export table: esbuild runs with `--global-name=App`, so `App.x` exists only
  for names `app.ts` EXPORTS, and a handler that is referenced but not exported
  is a silently dead button -- no build error, nothing red, just a control that
  does nothing when clicked. (This is also what caught `dlMintLink` building its
  handler as a string literal during the conversion.)

`copy.ts` is bundled **without** `--global-name`: index.html loads `app.js` then
`copy.js`, and a second global name would overwrite `window.App` with the copy
module's exports, killing every button on the page.
