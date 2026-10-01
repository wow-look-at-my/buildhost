# Admin dashboard frontend

The admin dashboard's browser code is TypeScript under `internal/admin/frontend/src/`. It compiles to `internal/admin/static/*.js`, which are **gitignored build artifacts**. A `//go:generate` directive produces them, the same as the CA bundle and the sqlc and regex code. No JavaScript is committed. The TypeScript is the only source.

```
internal/admin/frontend/src/app.ts    # the whole dashboard (router, pages, API calls)
internal/admin/frontend/src/html.ts   # the escaping HTML builder app.ts renders through
internal/admin/frontend/src/copy.ts   # the <copy-btn> custom element
internal/admin/frontend/src/types.ts  # the admin API's response shapes
```

## Building

`go-toolchain --generate <hash>` materializes everything, including this. To rebuild just the frontend (what the directive runs):

```bash
./scripts/build-admin-frontend.sh
```

Or from the frontend directory, `npm run build` type-checks and bundles, and `npm run check` runs `tsc --noEmit` alone. `npm ci` is needed once only. The script runs it for you when `node_modules` is absent.

npm is therefore required to build buildhost from a clean tree. That is the deliberate trade: the alternative -- committing the output so a Node-less build works -- is exactly what produced the drift described below.

## Why the embed names every file

```go
//go:embed static/index.html static/style.css static/app.js static/copy.js
```

A `static/*` wildcard still matches `index.html` and `style.css`. A build that skipped generate therefore compiles clean and serves a dashboard whose every script 404s. That is a blank page, and a user finds it. A named generated file turns the same mistake into a compile error (`pattern static/app.js: no matching files found`).

## The drift this layout exists to prevent

A conversion to TypeScript never took effect. It added a partial `src/app.ts` while the hand-written `static/app.js` kept shipping. Both were committed. The two then diverged for months. The live bundle grew the Retention page, the project tree, token CRUD, temp download links and the `/tap.git` fix. The TypeScript source had none of them. The documented "edit the TS, rebuild, commit" workflow cannot reproduce the shipped artifact. A generate from that source silently regresses the dashboard.

Two properties keep it from recurring, and both are load-bearing:

- **The output is generated, never committed.** There is no second copy to drift from. `internal/admin/static/*.js` is in `.gitignore`.
- **The tests read the generated bundle**, not the source (`internal/admin/static_test.go`). Chief among them is `TestAdminStaticInlineHandlersAreExported`. It reads every `onclick="App.x(...)"` reference in the rendered markup. It cross-checks each one against the bundle's export table. esbuild runs with `--global-name=App`, so `App.x` exists only for a name `app.ts` EXPORTS. A handler that is referenced but not exported is a silently dead button. There is no build error and nothing red, only a control that does nothing when clicked. This test also caught `dlMintLink`, which built its handler as a string literal during the conversion.

`copy.ts` is bundled **without** `--global-name`. index.html loads `app.js` and then `copy.js`. A second global name overwrites `window.App` with the copy module's exports, which kills every button on the page.
