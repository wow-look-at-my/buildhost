# The SHIPPED binary must render the route table docs/routes.txt records. The
# unit test in internal/routescheck renders in process, which cannot see a
# binary that fails to enumerate what it serves.
#
# see docs/testing.md

tests:
	- desc: the built binary renders a route table that covers the REST API
	  cmd: |
		set -eu
		out="$(mktemp)"
		"$BUILDHOST_BIN" routes > "$out"
		test -s "$out" || { echo "route render produced no output" >&2; exit 1; }
		grep -q '^/api/v1' "$out" || {
			echo "route render contains no /api/v1 routes" >&2
			echo "routes must register in init(), never inside auth.OnReady()" >&2
			exit 1
		}
		echo "routes-rendered"
	  outputs:
		stdout:
			- "routes-rendered"

	- desc: docs/routes.txt matches what the built binary renders
	  cmd: |
		set -eu
		out="$(mktemp)"
		"$BUILDHOST_BIN" routes > "$out"
		if ! diff -u --label "docs/routes.txt (committed)" --label "buildhost routes (built binary)" docs/routes.txt "$out"; then
			echo "docs/routes.txt is stale. Regenerate it and commit the result:" >&2
			echo "    go-toolchain && ./build/buildhost routes > docs/routes.txt" >&2
			echo "  (or, without a built binary:  UPDATE_ROUTES_GOLDEN=1 go-toolchain)" >&2
			exit 1
		fi
		echo "routes-golden-current"
	  outputs:
		stdout:
			- "routes-golden-current"
