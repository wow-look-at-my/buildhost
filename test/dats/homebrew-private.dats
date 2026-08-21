# The DOCUMENTED private Homebrew flow, executed verbatim, and what the
# authenticated tap must then hold. Split from the public suite because the
# docs say the authenticated tap REPLACES the public one, so the workflow
# untaps between the two.
#
# The brew commands come from README.md through scripts/brew-doc-flows.sh; the
# public suite is where the docs themselves are checked for agreement.
#
# see docs/formats/brew-tap.md

shared:
	files:
		start.sh: |
			# Run the documented private flow. Writes $ENV_FILE.
			set -eu
			WORK="$(dirname "$ENV_FILE")"
			"$REPO/scripts/brew-doc-flows.sh" private "$BREW_HOST" > "$WORK/private.sh"
			echo "--- documented private flow, executed verbatim ---"
			sed 's|x:[^@]*@|x:***@|' "$WORK/private.sh"
			TOKEN="$BUILDHOST_TOKEN" bash -euo pipefail "$WORK/private.sh"
			echo "TAP=$(brew --repository pazer/build)" > "$ENV_FILE"

setup: env ENV_FILE={shared.env} REPO="$PWD" sh {shared.start.sh}

tests:
	- desc: the privately installed binary executes
	  cmd: myapp
	  outputs:
		stdout:
			- "buildhost-homebrew-private-ok"

	# `brew update` refreshes a tap by fetching its git remote, and for the
	# authenticated tap the credentials live in that stored URL. Proving the
	# refetch works is the cheap half of a full brew update.
	- desc: the authenticated tap refetches with its stored credentials
	  cmd: |
		set -eu
		. {shared.env}
		remote="$(git -C "$TAP" remote get-url origin)"
		case "$remote" in
			*"/private/tap.git") ;;
			*) echo "tap remote is not the private tap URL" >&2; exit 1 ;;
		esac
		git -C "$TAP" fetch origin
		echo "refetch-ok"
	  outputs:
		stdout:
			- "refetch-ok"

	# One syntactically broken formula -- `class 7zip < Formula` from a
	# digit-leading project name -- surfaces to users as an ".rb: syntax error"
	# and can break evaluation of the whole tap. The tap here is the superset
	# one, so this covers every formula the server can serve.
	- desc: every formula in the authenticated tap is valid Ruby
	  cmd: |
		set -eu
		. {shared.env}
		ls "$TAP/Formula" > formulas.txt
		if grep -qx '7zip.rb' formulas.txt; then
			echo "digit-leading project 7zip must be excluded: brew cannot load it" >&2; exit 1
		fi
		grep -qx 'dotted.app.rb' formulas.txt || {
			echo "the dotted public project is missing from the tap" >&2; cat formulas.txt >&2; exit 1; }
		while read -r f; do
			brew ruby -- -c "$TAP/Formula/$f" | grep -q 'Syntax OK' \
				|| { echo "formula $f is not valid Ruby" >&2; exit 1; }
		done < formulas.txt
		echo "formulas=$(wc -l < formulas.txt | tr -d ' ') all parse"
	  outputs:
		stdout:
			- "all parse"
