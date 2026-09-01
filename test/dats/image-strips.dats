# The SHIPPED IMAGE must strip on download, serve symbols, and honor debug=1.
#
# This exists because stripping stopped happening and nobody noticed for weeks:
# it shelled out to strip(1)/objcopy(1), which the distroless image does not
# ship. Every download went out unstripped and fmt=symbols could not work. The
# unit tests all passed, because the runner has binutils and the container does
# not. Only a check against the real image sees this.
#
# The workflow builds the image, starts it on port 8080, and compiles the ELF
# fixture this uploads -- the shipped binary is an APE, and an APE is not an
# ELF for the stripper to work on.
#
# It bootstraps its token by exec'ing into the running container, and the
# sandbox exposes no docker socket -- by design, since reaching the daemon is
# reaching the whole host. So a workflow runs it with --no-sandbox.
#
# see docs/formats/stripping.md

shared:
	files:
		start.sh: |
			# Publish the fixture to the running container. Writes $ENV_FILE.
			set -eu
			WORK="$(dirname "$ENV_FILE")"
			test -n "${STRIP_FIXTURE:-}" || { echo "STRIP_FIXTURE must name an unstripped ELF" >&2; exit 1; }
			BASE="http://localhost:8080"
			STATIC="http://static.localhost:8080"
			# The binary is an APE, so it starts through the image's busybox
			# shell -- a direct exec answers "exec format error".
			TOKEN="$(docker compose -f "$REPO/docker-compose.ci.yml" exec -T buildhost \
				sh /usr/local/bin/buildhost bootstrap --name image-e2e | tail -1 | tr -d '\r')"
			test -n "$TOKEN" || { echo "no token from bootstrap inside the container" >&2; exit 1; }
			auth() { curl -fsS -H "Authorization: Bearer $TOKEN" "$@"; }
			auth -X POST "$BASE/api/v1/projects" -H 'Content-Type: application/json' \
				-d '{"name":"image-strip-e2e","versioning":"auto","is_private":false}' >/dev/null
			VERSION="$(auth -X POST "$BASE/api/v1/projects/image-strip-e2e/releases" \
				-H 'Content-Type: application/json' -d '{"git_branch":"master"}' | jq -r .version)"
			auth -X PUT --data-binary "@$STRIP_FIXTURE" \
				"$BASE/api/v1/projects/image-strip-e2e/releases/$VERSION/artifacts/linux/amd64?kind=binary" >/dev/null
			auth -X POST "$BASE/api/v1/projects/image-strip-e2e/releases/$VERSION/publish" >/dev/null
			# Quoted, because a test SOURCES this file and the query string
			# carries '&' -- unquoted, the shell backgrounds the assignment
			# and every value after it is unset.
			{
				echo "STATIC='$STATIC'"
				echo "QUERY='project=image-strip-e2e&v=$VERSION&os=linux&arch=amd64'"
				echo "FIXTURE='$STRIP_FIXTURE'"
				echo "WORK='$WORK'"
			} > "$ENV_FILE"

		# -L matters: /file canonicalizes its query with a 301, so an
		# unredirected request returns the redirect body, not the artifact.
		sections.sh: |
			set -eu
			readelf -SW "$1" | sed -n 's/^[[:space:]]*\[[[:space:]]*[0-9]\+\][[:space:]]\+\([^[:space:]]\+\).*/\1/p'

setup: env ENV_FILE={shared.env} REPO="$PWD" sh {shared.start.sh}

tests:
	- desc: the download comes back smaller than the upload
	  cmd: |
		set -eu
		. {shared.env}
		curl -fsSL -o out.bin "$STATIC/file?$QUERY&fmt=raw"
		up="$(wc -c < "$FIXTURE")"
		down="$(wc -c < out.bin)"
		echo "uploaded=$up downloaded=$down"
		test "$down" -lt "$up" || { echo "the shipped image did not strip" >&2; exit 1; }
		echo "stripped"
	  outputs:
		stdout:
			- "stripped"

	# Checked against the SECTION TABLE, not by searching for the string: the
	# section-name string table keeps every name it ever held, so ".debug_info"
	# is still present as BYTES in a correctly stripped file.
	- desc: the stripped download is a usable ELF with its debug sections gone
	  cmd: |
		set -eu
		. {shared.env}
		curl -fsSL -o out.bin "$STATIC/file?$QUERY&fmt=raw"
		test "$(head -c 4 out.bin | od -An -tx1 | tr -d ' \n')" = "7f454c46"
		sh {shared.sections.sh} out.bin > sections.txt
		for gone in .symtab .debug_info .debug_line; do
			if grep -qx -- "$gone" sections.txt; then echo "still has $gone" >&2; exit 1; fi
		done
		for want in .text .rodata; do
			grep -qx -- "$want" sections.txt || { echo "lost $want -- it could not run" >&2; exit 1; }
		done
		echo "elf-stripped-clean"
	  outputs:
		stdout:
			- "elf-stripped-clean"

	- desc: the header advertises symbols for this artifact
	  cmd: |
		set -eu
		. {shared.env}
		curl -fsSL -D - -o /dev/null "$STATIC/file?$QUERY&fmt=raw" | tr -d '\r' | grep -i '^x-debug-symbols:'
	  outputs:
		stdout:
			- "available"

	# This endpoint could not work in the distroless image before: it required
	# objcopy.
	- desc: fmt=symbols serves a file that carries the debug info
	  cmd: |
		set -eu
		. {shared.env}
		curl -fsSL -o symbols.bin "$STATIC/file?$QUERY&fmt=symbols"
		sh {shared.sections.sh} symbols.bin | grep -qx -- '.debug_info'
		echo "symbols-served"
	  outputs:
		stdout:
			- "symbols-served"

	- desc: debug=1 opts out and returns exactly the uploaded bytes
	  cmd: |
		set -eu
		. {shared.env}
		curl -fsSL -o full.bin "$STATIC/file?$QUERY&fmt=raw&debug=1"
		cmp "$FIXTURE" full.bin
		echo "bytes-identical"
	  outputs:
		stdout:
			- "bytes-identical"
