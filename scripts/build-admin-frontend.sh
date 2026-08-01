#!/usr/bin/env bash
# Build the admin dashboard's TypeScript into the static/ files that go:embed bakes
# into the binary. The outputs are gitignored build artifacts, so this runs as a
# //go:generate directive like every other generated input -- a build that skips
# generate ships an admin dashboard with no JavaScript, which is what happened when
# the built JS was un-committed with nothing left to rebuild it.
set -euo pipefail

cd "$(dirname "$0")/../internal/admin/frontend"

# npm ci needs the lockfile and is reproducible; fall back to install if the tree
# was vendored some other way. --silent keeps the go-toolchain log readable.
if [ -f package-lock.json ]; then
	npm ci --silent --no-audit --no-fund
else
	npm install --silent --no-audit --no-fund
fi

npm run build --silent

# esbuild writes what index.html loads; a missing output here means the binary would
# embed an admin page whose scripts 404, so fail loudly instead.
for f in app.js copy.js; do
	test -s "../static/$f" || { echo "build-admin-frontend: ../static/$f was not produced" >&2; exit 1; }
done
