#!/usr/bin/env bash
# Build the admin dashboard's TypeScript into the JS that internal/admin embeds.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
frontend="$here/internal/admin/frontend"

if ! command -v npm >/dev/null 2>&1; then
    echo "build-admin-frontend: npm is required to build internal/admin/static/*.js" >&2
    echo "  (the admin dashboard's JS is generated from $frontend/src, never committed)" >&2
    exit 1
fi

cd "$frontend"

# npm ci needs the lockfile; reuse an existing tree so repeat generates are fast.
if [ ! -d node_modules ]; then
    npm ci --silent
fi

# Type-check first, then bundle.
bin="$frontend/node_modules/.bin"
"$bin/tsc" --noEmit
"$bin/esbuild" src/app.ts --bundle --outfile=../static/app.js --format=iife --global-name=App --target=es2020
"$bin/esbuild" src/copy.ts --bundle --outfile=../static/copy.js --format=iife --target=es2020

for f in ../static/app.js ../static/copy.js; do
    [ -s "$f" ] || { echo "build-admin-frontend: $f was not produced" >&2; exit 1; }
done

echo "> built admin frontend -> internal/admin/static/{app,copy}.js"
