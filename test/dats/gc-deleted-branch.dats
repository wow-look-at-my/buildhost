# The shipped binary's reclaim pass must carry the deleted-branch reason beside
# the keep-N and abandoned figures it already reports. The rule itself is gated
# by the unit tests in internal/retention, which drive the real pass with only
# the branch lister faked: a deterministic CLI case cannot seed a 30-day-old
# build on a branch that GitHub has forgotten, because the GitHub API root is a
# package variable and not env-configurable.
#
# What this suite does prove is the CLI-visible half: buildhost gc really runs
# reclaim against a scratch database, and its report names the reason, so a
# future change that silently drops the reason fails here.
#
# $BUILDHOST_BIN comes from the workflow.
#
# see docs/testing.md, internal/retention/deletedbranch.go

tests:
	- desc: buildhost gc reports the deleted-branch reason beside the existing ones
	  cmd: |
		set -eu
		BIN="${BUILDHOST_BIN:-build/buildhost}"
		test -x "$BIN" || { echo "not executable: $BIN" >&2; exit 1; }
		WORK="$(mktemp -d)"
		mkdir -p "$WORK/data"
		BUILDHOST_DATA_DIR="$WORK/data"; export BUILDHOST_DATA_DIR
		BUILDHOST_DB_PATH="$WORK/data/buildhost.db"; export BUILDHOST_DB_PATH

		"$BIN" gc > "$WORK/gc.txt" 2>&1 || {
			echo "buildhost gc failed on a scratch database:" >&2
			cat "$WORK/gc.txt" >&2
			exit 1
		}

		grep -q 'DRY RUN' "$WORK/gc.txt" || {
			echo "gc did not run report-only by default" >&2; cat "$WORK/gc.txt" >&2; exit 1; }
		grep -q 'past keep-N' "$WORK/gc.txt" || {
			echo "the keep-N figure is missing from the report" >&2; cat "$WORK/gc.txt" >&2; exit 1; }
		grep -q 'abandoned' "$WORK/gc.txt" || {
			echo "the abandoned figure is missing from the report" >&2; cat "$WORK/gc.txt" >&2; exit 1; }
		grep -q 'deleted-branch builds:' "$WORK/gc.txt" || {
			echo "the report does not name the deleted-branch reason" >&2; cat "$WORK/gc.txt" >&2; exit 1; }
		grep -q 'on deleted branches' "$WORK/gc.txt" || {
			echo "the release count does not break out builds on deleted branches" >&2; cat "$WORK/gc.txt" >&2; exit 1; }
		echo "gc-reports-deleted-branch"
	  outputs:
		stdout:
			- "gc-reports-deleted-branch"

	- desc: two gc passes over an unchanged database agree
	  cmd: |
		set -eu
		BIN="${BUILDHOST_BIN:-build/buildhost}"
		test -x "$BIN" || { echo "not executable: $BIN" >&2; exit 1; }
		WORK="$(mktemp -d)"
		mkdir -p "$WORK/data"
		BUILDHOST_DATA_DIR="$WORK/data"; export BUILDHOST_DATA_DIR
		BUILDHOST_DB_PATH="$WORK/data/buildhost.db"; export BUILDHOST_DB_PATH

		"$BIN" gc > "$WORK/first.txt" 2>&1
		"$BIN" gc > "$WORK/second.txt" 2>&1
		if ! diff -u "$WORK/first.txt" "$WORK/second.txt"; then
			echo "two gc passes over an unchanged database disagreed" >&2
			exit 1
		fi
		grep -q 'deleted-branch builds:' "$WORK/second.txt"
		echo "gc-is-stable"
	  outputs:
		stdout:
			- "gc-is-stable"
