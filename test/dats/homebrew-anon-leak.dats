# The ANONYMOUS tap must not know a private project exists, and an
# unauthorized formula request must be a clean HTTP error.
#
# A 200 with a JSON body saved as formula.rb is exactly the ".rb: syntax error"
# class of failure users hit, so the content type matters as much as the code.
# The private project here is myrepo/myapp, which folds to myrepo-myapp.
#
# $BREW_HOST comes from the workflow, which starts the server and publishes.
#
# see docs/formats/brew-tap.md, docs/security/tokens-and-links.md

shared:
	files:
		start.sh: |
			set -eu
			WORK="$(dirname "$ENV_FILE")"
			git clone "http://$BREW_HOST/tap.git" "$WORK/tap"
			echo "TAP='$WORK/tap'" > "$ENV_FILE"

setup: env ENV_FILE={shared.env} sh {shared.start.sh}

tests:
	- desc: the anonymous tap serves the public formula and not the private one
	  cmd: |
		set -eu
		. {shared.env}
		ls "$TAP/Formula" > formulas.txt
		grep -qx 'go-toolchain.rb' formulas.txt || {
			echo "the public formula is missing" >&2; cat formulas.txt >&2; exit 1; }
		if grep -qx 'myrepo-myapp.rb' formulas.txt; then
			echo "the anonymous tap LEAKS the private formula file" >&2; exit 1
		fi
		echo "public-only"
	  outputs:
		stdout:
			- "public-only"

	# Not just the formula file: the private project's NAME must appear in no
	# file the anonymous clone carries.
	- desc: no file in the anonymous tap names the private project
	  cmd: |
		set -eu
		. {shared.env}
		if grep -rl --exclude-dir=.git 'myrepo' "$TAP"; then
			echo "the anonymous tap leaks the private project name" >&2; exit 1
		fi
		echo "no-leak"
	  outputs:
		stdout:
			- "no-leak"

	# The literal slash-namespaced path names the project exactly, so it
	# answers 401 like the legacy path. The FOLDED name is different: it
	# resolves back to its project only for a request that may read it, so an
	# anonymous probe stays indistinguishable from nonexistent rather than
	# confirming a private project exists.
	- desc: unauthorized formula paths return clean errors, never a fake formula body
	  cmd: |
		set -eu
		. {shared.env}
		check() {
			out="$(curl -s -o /dev/null -w '%{http_code} %{content_type}' "http://$BREW_HOST$1")"
			echo "GET $1 -> $out"
			case "$out" in
				"$2"*) ;;
				*) echo "want HTTP $2 for $1" >&2; exit 1 ;;
			esac
			case "$out" in
				*ruby*) echo "error response served as ruby for $1" >&2; exit 1 ;;
			esac
		}
		check /myrepo/myapp 401
		check /Formula/myrepo/myapp.rb 401
		check /Formula/myrepo-myapp.rb 404
		check /Formula/7zip.rb 404
		echo "clean-errors"
	  outputs:
		stdout:
			- "clean-errors"
