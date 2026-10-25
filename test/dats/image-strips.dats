#
# This exists because stripping stopped happening and nobody noticed for weeks.
# It shelled out to strip(1)/objcopy(1), which the distroless image does not
# ship. Every download went out unstripped and fmt=symbols could not work. The
# unit tests all passed, because the runner has binutils and the container does
# not. Only a check against the real image sees this.
#
# Needs curl, jq, readelf and the docker CLI, so a workflow runs it
# --no-sandbox.
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
			TOKEN="$(docker compose -f "$REPO/docker-compose.ci.yml" exec -T buildhost \
				/usr/local/bin/buildhost bootstrap --name image-e2e | tail -1 | tr -d '\r')"
			test -n "$TOKEN" || { echo "no token from bootstrap inside the container" >&2; exit 1; }
			auth() { curl -fsS -H "Authorization: Bearer $TOKEN" "$@"; }
			auth -X POST "$BASE/api/v1/projects" -H 'Content-Type: application/json' \
				-d '{"name":"image-strip-e2e","versioning":"auto","is_private":false}' >/dev/null
			VERSION="$(auth -X POST "$BASE/api/v1/projects/image-strip-e2e/releases" \
				-H 'Content-Type: application/json' -d '{"git_branch":"master"}' | jq -r .version)"
			auth -X PUT --data-binary "@$STRIP_FIXTURE" \
				"$BASE/api/v1/projects/image-strip-e2e/releases/$VERSION/artifacts/linux/amd64?kind=binary" >/dev/null
			auth -X POST "$BASE/api/v1/projects/image-strip-e2e/releases/$VERSION/publish" >/dev/null

			# The fat APE this build produced. UPX cannot pack one without turning it into
			# a plain ELF, so the download must come back as the upload.
			APE="$REPO/build/buildhost"
			if [ ! -f "$APE" ]; then echo "the build produced no APE at $APE" >&2; exit 1; fi
			auth -X POST "$BASE/api/v1/projects" -H 'Content-Type: application/json' \
			-d '{"name":"image-ape-e2e","versioning":"auto","is_private":false}' >/dev/null
			APE_VERSION="$(auth -X POST "$BASE/api/v1/projects/image-ape-e2e/releases" \
			-H 'Content-Type: application/json' -d '{"git_branch":"master"}' | jq -r .version)"
			auth -X PUT --data-binary "@$APE" \
			"$BASE/api/v1/projects/image-ape-e2e/releases/$APE_VERSION/artifacts/linux/amd64?kind=binary" >/dev/null
			auth -X POST "$BASE/api/v1/projects/image-ape-e2e/releases/$APE_VERSION/publish" >/dev/null
			# Quoted, because a test SOURCES this file and the query string
			# carries '&' -- unquoted, the shell backgrounds the assignment
			# and every value after it is unset.
			{
				echo "STATIC='$STATIC'"
				echo "QUERY='project=image-strip-e2e&v=$VERSION&os=linux&arch=amd64'"
				echo "APE_QUERY='project=image-ape-e2e&v=$APE_VERSION&os=linux&arch=amd64'"
				echo "APE='$APE'"
				echo "FIXTURE='$STRIP_FIXTURE'"
				echo "WORK='$WORK'"
			} > "$ENV_FILE"

		sections.sh: |
			set -eu
			readelf -SW "$1" | sed -n 's/^[[:space:]]*\[[[:space:]]*[0-9]\+\][[:space:]]\+\([^[:space:]]\+\).*/\1/p'

setup: env ENV_FILE={shared.env} REPO="$PWD" sh {shared.start.sh}

# The tests run at the same time. Each one writes its files in its own
# directory, or one test reads a file that another test still writes.
tests:
	- desc: the download comes back smaller than the upload
	  cmd: |
		set -eu
		. {shared.env}
		cd "$(mktemp -d)"
		curl -fsSL -o out.bin "$STATIC/file?$QUERY&fmt=raw"
		up="$(wc -c < "$FIXTURE")"
		down="$(wc -c < out.bin)"
		echo "uploaded=$up downloaded=$down"
		test "$down" -lt "$up" || { echo "the shipped image did not strip" >&2; exit 1; }
		echo "stripped"
	  outputs:
		stdout:
			- "stripped"

	# Checked against the SECTION TABLE, not by searching for the string. The
	# section-name string table keeps every name it ever held. So ".debug_info"
	# is still present as BYTES in a correctly stripped file.
	- desc: the stripped download is a usable ELF with its debug sections gone
	  cmd: |
		set -eu
		. {shared.env}
		cd "$(mktemp -d)"
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

	# gzip and the identity response must decode to the same bytes, and the brotli
	# body must be smaller than the identity one.
	- desc: every transfer coding decodes to the served payload
	  cmd: |
		set -eu
		. {shared.env}
		cd "$(mktemp -d)"
		curl -fsSL -o identity.bin "$STATIC/file?$QUERY&fmt=raw"
		curl -fsSL -H 'Accept-Encoding: gzip' -o gz.bin "$STATIC/file?$QUERY&fmt=raw"
		test "$(curl -fsSL -D - -o /dev/null -H 'Accept-Encoding: gzip' "$STATIC/file?$QUERY&fmt=raw" | tr -d '\r' | sed -n 's/^[Cc]ontent-[Ee]ncoding: *//p')" = "gzip"
		gzip -dc gz.bin > gz-decoded.bin
		cmp identity.bin gz-decoded.bin
		curl -fsSL -D br-headers.txt -o br.bin -H 'Accept-Encoding: br' "$STATIC/file?$QUERY&fmt=raw"
		test "$(tr -d '\r' < br-headers.txt | sed -n 's/^[Cc]ontent-[Ee]ncoding: *//p')" = "br"
		test "$(wc -c < br.bin)" -lt "$(wc -c < identity.bin)"
		echo "codings-agree"
	  outputs:
		stdout:
			- "codings-agree"

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
		cd "$(mktemp -d)"
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
		cd "$(mktemp -d)"
		curl -fsSL -o full.bin "$STATIC/file?$QUERY&fmt=raw&debug=1"
		cmp "$FIXTURE" full.bin
		echo "bytes-identical"
	  outputs:
		stdout:
			- "bytes-identical"

	- desc: an APE is served exactly as uploaded, twice
	  cmd: |
		set -eu
		. {shared.env}
		cd "$(mktemp -d)"
		curl -fsSL -o ape1.bin "$STATIC/file?$APE_QUERY&fmt=raw"
		curl -fsSL -o ape2.bin "$STATIC/file?$APE_QUERY&fmt=raw"
		cmp "$APE" ape1.bin
		cmp ape1.bin ape2.bin
		echo "ape-verbatim"
	  outputs:
		stdout:
			- "ape-verbatim"
