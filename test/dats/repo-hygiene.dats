# A tracked build artifact drifts from the source it claims to come from,
# silently, for as long as nobody rebuilds -- which is how the admin dashboard
# shipped a bundle nothing in frontend/src produced. These run after generate,
# so a tracked generated file shows up as a dirty tree.
#
# see docs/no-committed-build-artifacts.md

tests:
	# Drift: a committed artifact whose source no longer produces it.
	- desc: running generate leaves every tracked file unchanged
	  cmd: |
		set -eu
		if git diff --quiet; then echo "clean tree"; exit 0; fi
		echo "Running generate modified files that are committed to git."
		echo "A tracked generated file has drifted from the source it claims to"
		echo "be built from. Gitignore it and let its //go:generate produce it."
		git diff --stat
		exit 1
	  outputs:
		stdout:
			- "clean tree"

	# Committed-at-all: a tracked file that .gitignore calls build output.
	# Catches the artifact even while it still happens to match its source.
	- desc: no tracked file is gitignored build output
	  cmd: |
		set -eu
		ignored="$(git ls-files -i -c --exclude-standard)"
		if [ -n "$ignored" ]; then
			echo "These build artifacts are committed to git:"
			echo "$ignored"
			echo "Remove them from the index (git rm --cached)."
			exit 1
		fi
		echo "no tracked artifacts"
	  outputs:
		stdout:
			- "no tracked artifacts"

	- desc: an inline action script carries no stacked comments
	  cmd: node .github/scripts/no-stacked-comments.ts .github/actions/*/action.yml .github/workflows/*.yml
	  outputs:
		stdout:
			- "no stacked inline-script comments"

	- desc: compression-level accepts an integer and nothing else
	  cmd: bash .github/scripts/compression-opts-test.sh
