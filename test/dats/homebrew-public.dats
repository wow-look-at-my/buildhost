# What `brew install pazer/build/<project>` must leave on disk, and that it
# runs. The install itself is a workflow step, because it needs the real brew
# and the tap served by a real server; these are the assertions about what that
# install produced.
#
# Runs on the host (--no-sandbox): brew's prefix is outside any sandbox this
# would get, and the point is what brew did to a real file.
#
# see docs/formats/brew-tap.md

tests:
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
