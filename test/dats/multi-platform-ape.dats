# One APE is ONE artifact row that every covered platform folds onto, and a
# multi-platform claim without APE magic is refused. One server serves the
# whole file: setup publishes the APE, the tests only ask questions about it.
#
# Needs curl and jq, which is why a workflow runs it rather than the `dats/`
# phase: the sandbox binds the host's own tool trees, and go-toolchain's
# fallback image has neither.
#
# see docs/multi-platform-artifacts.md

shared:
	files:
		start.sh: |
			# Publish one APE and leave the server running. Writes $ENV_FILE.
			set -eu
			WORK="$(dirname "$ENV_FILE")"
			BIN="${BUILDHOST_BIN:-}"
			if [ -z "$BIN" ]; then
				if [ -f "$REPO/build/buildhost" ]; then BIN="$REPO/build/buildhost"; fi
			fi
			test -n "$BIN" || { echo "no buildhost binary: build one first" >&2; exit 1; }
			# An APE cannot be exec'd: its header is a shell script, and
			# nothing here registers an APE binfmt handler. A native ELF is the
			# opposite and must not go through a shell.
			if head -c 2 "$BIN" | grep -q MZ; then RUN="sh $BIN"; else RUN="$BIN"; fi
			BUILDHOST_DATA_DIR="$WORK/data"; export BUILDHOST_DATA_DIR
			BUILDHOST_DB_PATH="$WORK/data/buildhost.db"; export BUILDHOST_DB_PATH
			started=""
			for off in 0 1 2 3 4 5 6 7 8 9; do
				PORT=$(( 18100 + 2 * off ))
				BUILDHOST_LISTEN_ADDR="127.0.0.1:$PORT"; export BUILDHOST_LISTEN_ADDR
				# An empty value means the default :9090, shared with whatever
				# else is on this host.
				BUILDHOST_ADMIN_LISTEN_ADDR="127.0.0.1:$(( PORT + 1 ))"; export BUILDHOST_ADMIN_LISTEN_ADDR
				BASE="http://localhost:$PORT"
				rm -rf "$BUILDHOST_DATA_DIR"
				TOKEN="$($RUN bootstrap --name dats | tail -1)"
				# setsid: the server leads its own process group, so teardown
				# takes the whole tree down. An APE runs under a shell, and
				# killing that shell alone orphans the server on its port.
				setsid $RUN serve > "$WORK/server.log" 2>&1 &
				echo "$!" > "$WORK/server.pid"
				for _ in $(seq 30); do
					if curl -fsS "$BASE/healthz" >/dev/null 2>&1; then started=yes; break; fi
					sleep 1
				done
				if [ -n "$started" ]; then break; fi
				kill -- "-$(cat "$WORK/server.pid")" 2>/dev/null || true
			done
			test -n "$started" || { echo "no server came up:" >&2; cat "$WORK/server.log" >&2; exit 1; }
			auth() { curl -fsS -H "Authorization: Bearer $TOKEN" "$@"; }
			# MZqFpD is the magic every Actually Portable Executable opens with.
			{ printf 'MZqFpD'; head -c 4096 /dev/urandom; } > "$WORK/ape.bin"
			{ printf '\177ELF'; head -c 4096 /dev/urandom; } > "$WORK/plain.bin"
			auth -X POST "$BASE/api/v1/projects" -H 'Content-Type: application/json' \
				-d '{"name":"ape-e2e","versioning":"auto"}' >/dev/null
			VERSION="$(auth -X POST "$BASE/api/v1/projects/ape-e2e/releases" \
				-H 'Content-Type: application/json' -d '{"git_branch":"master"}' | jq -r .version)"
			auth -X PUT --data-binary "@$WORK/ape.bin" -H 'X-Artifact-Filename: ape-e2e' \
				"$BASE/api/v1/projects/ape-e2e/releases/$VERSION/artifacts/ape?platforms=linux/amd64,darwin/arm64,windows/amd64" \
				-o "$WORK/artifact.json"
			jq -e '.platforms | length == 3' "$WORK/artifact.json" >/dev/null
			jq -e '.exe_format == "ape"' "$WORK/artifact.json" >/dev/null
			auth -X POST "$BASE/api/v1/projects/ape-e2e/releases/$VERSION/publish" \
				-H 'Content-Type: application/json' -d '{}' >/dev/null
			{
				echo "PORT=$PORT"
				echo "BASE=$BASE"
				echo "TOKEN=$TOKEN"
				echo "VERSION=$VERSION"
				echo "WORK=$WORK"
			} > "$ENV_FILE"

setup: env ENV_FILE={shared.env} REPO="$PWD" sh {shared.start.sh}
teardown: sh -c '. {shared.env}; kill -- "-$(cat "$WORK/server.pid")" 2>/dev/null; true'

tests:
	- desc: an APE upload is one row carrying every platform it covers
	  cmd: |
		set -eu
		. {shared.env}
		curl -fsS -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/projects/ape-e2e/releases/$VERSION" > {outputs.rel.json}
		echo "rows=$(jq '.artifacts | length' {outputs.rel.json}) artifacts"
	  outputs:
		stdout:
			- "rows=1 artifacts"

	- desc: every covered platform redirects to the same URL and the same bytes
	  cmd: |
		set -eu
		. {shared.env}
		want="$(sha256sum "$WORK/ape.bin" | awk '{print $1}')"
		first=""
		# macOS/aarch64 is the alias spelling: it must fold onto the same URL.
		for spec in linux/amd64 darwin/arm64 windows/amd64 macOS/aarch64; do
			os="${spec%%/*}"; arch="${spec##*/}"
			loc="$(curl -fsS -o /dev/null -w '%{redirect_url}' \
				"http://dl.localhost:$PORT/ape-e2e?os=$os&arch=$arch&v=$VERSION")"
			test -n "$loc"
			if [ -z "$first" ]; then first="$loc"; else
				test "$loc" = "$first" || { echo "$spec redirected to $loc, want $first"; exit 1; }
			fi
			got="$(curl -fsSL "$loc" | sha256sum | awk '{print $1}')"
			test "$got" = "$want" || { echo "$spec served $got, want $want"; exit 1; }
		done
		echo "one-url"
	  outputs:
		stdout:
			- "one-url"

	- desc: the release page shows one link with an APE badge
	  cmd: |
		set -eu
		. {shared.env}
		curl -fsS "$BASE/projects/ape-e2e/releases/$VERSION" > {outputs.release.html}
		grep -q 'badge badge-format' {outputs.release.html}
		grep -q 'APE: ' {outputs.release.html}
		grep -q 'linux/amd64, darwin/arm64, windows/amd64' {outputs.release.html}
		echo "raw-links=$(grep -c '>raw</a>' {outputs.release.html}) found"
	  outputs:
		stdout:
			- "raw-links=1 found"

	- desc: a multi-platform claim without APE magic is refused, storing nothing
	  cmd: |
		set -eu
		. {shared.env}
		auth() { curl -fsS -H "Authorization: Bearer $TOKEN" "$@"; }
		V2="$(auth -X POST "$BASE/api/v1/projects/ape-e2e/releases" \
			-H 'Content-Type: application/json' -d '{"git_branch":"master"}' | jq -r .version)"
		code="$(curl -sS -o reject.json -w '%{http_code}' -X PUT \
			--data-binary "@$WORK/plain.bin" -H "Authorization: Bearer $TOKEN" \
			"$BASE/api/v1/projects/ape-e2e/releases/$V2/artifacts/ape?platforms=linux/amd64,darwin/arm64")"
		echo "code=$code returned"
		echo "rows=$(auth "$BASE/api/v1/projects/ape-e2e/releases/$V2" | jq '.artifacts | length') artifacts"
	  outputs:
		stdout:
			- "code=400 returned"
			- "rows=0 artifacts"
