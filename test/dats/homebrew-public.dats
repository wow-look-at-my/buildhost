# The DOCUMENTED public Homebrew flow, executed verbatim, and what it must
# leave on disk.
#
# The brew commands are not written here: scripts/brew-doc-flows.sh extracts
# them from README.md and substitutes only the host, and this suite asserts the
# served /llms.txt agrees with them. A change that "fixes CI" without fixing
# the docs goes red instead of silently diverging -- the repo lived through
# that once, when Homebrew 6.0 broke the flow and the fix landed in CI only.
#
# The workflow starts the server and publishes the artifacts; $BUILDHOST_TOKEN,
# $BUILDHOST_BASE_URL and $BREW_HOST come from it.
#
# Runs on the host (--no-sandbox): brew's prefix is outside any sandbox this
# would get, and the point is what brew did to a real file.
#
# see docs/formats/brew-tap.md

shared:
	files:
		start.sh: |
			# Run the documented public flow. Writes $ENV_FILE.
			set -eu
			WORK="$(dirname "$ENV_FILE")"
			"$REPO/scripts/brew-doc-flows.sh" public "$BREW_HOST" > "$WORK/public.sh"
			echo "--- documented public flow, executed verbatim ---"
			cat "$WORK/public.sh"
			TOKEN="$BUILDHOST_TOKEN" bash -euo pipefail "$WORK/public.sh"
			echo "REPO=$REPO" > "$ENV_FILE"

setup: env ENV_FILE={shared.env} REPO="$PWD" sh {shared.start.sh}

tests:
	# One flow, two documents, zero drift: the blocks the server serves in
	# /llms.txt must be the blocks README.md documents.
	- desc: llms.txt documents the same brew flows as README.md
	  cmd: |
		set -eu
		. {shared.env}
		curl -fsS "$BUILDHOST_BASE_URL/llms.txt" > llms.txt
		# The served blocks are fenced with no language tag; the brew flows are
		# the ones that open with `brew tap`.
		awk '/^```/ { fence = !fence; if (fence) { buf = "" } else if (buf ~ /^brew tap /) { printf "%s", buf }; next }
			fence { buf = buf $0 "\n" }' llms.txt > llms-flows.txt
		test "$(grep -c '^brew tap ' llms-flows.txt)" = "2" || {
			echo "llms.txt: want exactly 2 brew flow blocks (public, private)" >&2; exit 1; }
		# llms.txt names the public host, so compare it after the same
		# substitution the extractor applies to README.md.
		sed -e 's|https://|http://|g' -e "s|brew\.pazer\.build|$BREW_HOST|g" llms-flows.txt > llms-local.txt
		"$REPO/scripts/brew-doc-flows.sh" public "$BREW_HOST" > readme-flows.txt
		"$REPO/scripts/brew-doc-flows.sh" private "$BREW_HOST" >> readme-flows.txt
		diff -u readme-flows.txt llms-local.txt
		echo "docs-agree"
	  outputs:
		stdout:
			- "docs-agree"

	# The tripwire against smuggling CI-only crutches into the executed
	# commands: every line is a plain brew command or a token export, and the
	# tap/trust/install skeleton is present.
	- desc: the documented flows are brew commands only, with tap, trust and install
	  cmd: |
		set -eu
		. {shared.env}
		for leg in public private; do
			"$REPO/scripts/brew-doc-flows.sh" "$leg" "$BREW_HOST" \
				| grep -v '^[[:space:]]*$' | grep -v '^#' > flow.txt
			if grep -qv '^\(brew\|export\) ' flow.txt; then
				echo "$leg flow has a non-brew/export line:" >&2; cat flow.txt >&2; exit 1
			fi
			for want in 'brew tap ' 'brew trust ' 'brew install '; do
				grep -q "^$want" flow.txt || { echo "$leg flow lost $want" >&2; exit 1; }
			done
		done
		echo "flow-shape-ok"
	  outputs:
		stdout:
			- "flow-shape-ok"

	# Homebrew's Cleaner chmods anything it does not recognize as a
	# shebang/ELF/Mach-O to 0444, and an APE rewrites itself in place on first
	# run, so it dies with "Permission denied" at 0555. The generated formula
	# opts out with skip_clean "bin"; this is what turns a regression in that
	# codegen red instead of shipping a binary users cannot run.
	- desc: the installed binary keeps both the execute and the write bit
	  cmd: |
		set -euo pipefail
		bin="$(brew --prefix pazer/build/go-toolchain)/bin/go-toolchain"
		ls -l "$bin"
		test -x "$bin" || { echo "not executable -- Homebrew's Cleaner chmods unrecognized files 0444"; exit 1; }
		test -w "$bin" || { echo "not writable -- an APE rewrites itself on first run"; exit 1; }
		echo "mode-ok"
	  outputs:
		stdout:
			- "mode-ok"

	# The user-facing claim is that the installed thing RUNS. On Linux this is
	# also where an APE assimilates itself, which is what makes the write bit
	# load-bearing. `version` exits 0 even when its update check cannot reach
	# GitHub, so this is not a network-flaky assertion.
	- desc: the publicly installed binary executes
	  cmd: go-toolchain version
	  outputs:
		stdout:
			- "Version:"

	# The same guarantees against the APE-SHAPED fixture, so the invariant
	# holds on macOS too (where go-toolchain's own artifact is a Mach-O brew
	# recognizes) and for anyone shipping a Cosmopolitan binary. The fixture
	# fails loudly at 0555 and cannot be executed at all at 0444.
	- desc: an APE-shaped formula installs, keeps its mode, and runs
	  cmd: |
		set -euo pipefail
		brew install pazer/build/ape-fixture
		bin="$(brew --prefix pazer/build/ape-fixture)/bin/ape-fixture"
		ls -l "$bin"
		test -x "$bin" || { echo "not executable (Cleaner chmods unrecognized files 0444)"; exit 1; }
		test -w "$bin" || { echo "not writable (an APE rewrites itself on first run)"; exit 1; }
		ape-fixture
	  outputs:
		stdout:
			- "buildhost-homebrew-ape-ok"
