#!/usr/bin/env bash
# One-time migration: the dats action replaced its freeform `args` input with a
# required, typed `tests` input (run-dats.ts rejects anything flag-shaped), and
# installs the sandbox backend itself, so callers pass only test paths.
set -euo pipefail

file=".github/workflows/ci.yml"
perl -pi -e 's/args: --no-sandbox test /tests: /' "$file"
echo "migrated: $(grep -c 'tests: test/dats' "$file") steps"
echo "remaining no-sandbox refs: $(grep -c 'no-sandbox' "$file" || true)"