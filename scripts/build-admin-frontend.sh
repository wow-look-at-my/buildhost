#!/usr/bin/env bash
# Build the admin dashboard's TypeScript into the JS that internal/admin embeds.
#
# The outputs (internal/admin/static/*.js) are gitignored BUILD ARTIFACTS: the
# TypeScript under internal/admin/frontend/src is the only source. This script is
# the //go:generate directive beside that embed, so `go-toolchain --generate`
# materializes them exactly like the CA bundle and the sqlc/regex code.
#
# It fails loudly rather than skipping: a dashboard that ships without its JS is
# a blank page, and that is precisely the failure that went unnoticed when the
# artifacts were committed instead of generated.
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

# `npm run build` type-checks first (tsc --noEmit), then bundles with esbuild.
npm run build --silent

for f in ../static/app.js ../static/copy.js; do
    [ -s "$f" ] || { echo "build-admin-frontend: $f was not produced" >&2; exit 1; }
done

echo "> built admin frontend -> internal/admin/static/{app,copy}.js"
