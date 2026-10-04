# The DOCUMENTED private Homebrew flow, executed verbatim, and what the
# authenticated tap must then hold. It runs after the public suite, on the
# public tap that suite added and installed from: the documented flow repoints
# that tap in place, and every formula installed through it must survive.
#
# The brew commands come from docs/homebrew.md through scripts/brew-doc-flows.sh; the
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
			TAP="$(brew --repository pazer/build)"
			for _ in $(seq 60); do
				git -C "$TAP" pull -q --ff-only
				[ "$(git -C "$TAP" log --format=%H -- Formula/myrepo-myapp.rb | wc -l)" -ge 2 ] && break
				sleep 1
			done
			HOMEBREW_NO_GITHUB_API=1 HOMEBREW_BUILDHOST_TOKEN="$BUILDHOST_TOKEN" brew version-install pazer/build/myrepo-myapp@0.9.0
			echo "TAP='$TAP'" > "$ENV_FILE"

setup:
	- cmd: env ENV_FILE={shared.env} REPO="$PWD" sh {shared.start.sh}
	  timeout: 10m

tests:
	- desc: formulae installed from the public tap stay installed across the switch
	  cmd: |
		set -eu
		brew list --formula --full-name > installed.txt
		for f in pazer/build/go-toolchain pazer/build/ape-fixture; do
			grep -qx "$f" installed.txt || { echo "$f was uninstalled by the switch" >&2; cat installed.txt >&2; exit 1; }
		done
		echo "kept-installed"
	  outputs:
		stdout:
			- "kept-installed"

	- desc: the privately installed binary executes
	  cmd: myapp
	  outputs:
		stdout:
			- "buildhost-homebrew-private-ok"

	# The extracted formula lands in the user's versions tap, which has no lib/,
	# so its download strategy must ride inside the formula.
	- desc: brew version-install installs an older private release through the token strategy
	  cmd: '"$(brew --prefix myrepo-myapp@0.9.0)/bin/myapp"'
	  outputs:
		stdout:
			- "buildhost-homebrew-private-0.9.0"

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

	# A single syntactically broken formula -- `class 7zip < Formula` from a
	# digit-leading project name -- surfaces to users as an ".rb: syntax error"
	# and can break evaluation of the whole tap.
	- desc: every formula in the authenticated tap is valid Ruby
	  cmd: |
		set -eu
		. {shared.env}
		(cd "$TAP/Formula" && find . -name '*.rb' | sed 's|^\./||' | sort) > formulas.txt
		if grep -q '^7zip' formulas.txt; then
			echo "digit-leading project 7zip must be excluded: brew cannot load it" >&2; exit 1
		fi
		grep -qx 'dotted.app.rb' formulas.txt || {
			echo "the dotted public project is missing from the tap" >&2; cat formulas.txt >&2; exit 1; }
		grep -qx 'myrepo-myapp.rb' formulas.txt || {
			echo "the private formula is missing from the tap" >&2; cat formulas.txt >&2; exit 1; }
		if grep -q '[@/]' formulas.txt; then
			echo "the tap must hold one formula per project, no versions" >&2; cat formulas.txt >&2; exit 1
		fi
		while read -r f; do
			brew ruby -- -c "$TAP/Formula/$f" | grep -q 'Syntax OK' \
				|| { echo "formula $f is not valid Ruby" >&2; exit 1; }
		done < formulas.txt
		echo "formulas=$(wc -l < formulas.txt | tr -d ' ') all parse"
	  outputs:
		stdout:
			- "all parse"
