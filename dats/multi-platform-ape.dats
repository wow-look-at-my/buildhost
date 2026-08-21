# One APE is ONE artifact row that every covered platform folds onto, and a
# multi-platform claim without APE magic is refused. Each test owns its server:
# the sandbox gives it a private loopback, so the port is free by construction.
#
# see docs/multi-platform-artifacts.md

shared:
	files:
		serve.sh: |
			# Start a server on $PORT under a fresh data dir, export TOKEN and
			# BASE, and wait until it answers. Sourced by every test here.
			set -euo pipefail
			# The binary is whatever this checkout built: the root build makes
			# build/buildhost, the matrix makes the fat APE. BUILDHOST_BIN wins.
			BIN="${BUILDHOST_BIN:-}"
			if [ -z "$BIN" ]; then
				for c in build/buildhost_cosmo_fat build/buildhost; do
					if [ -f "$c" ]; then BIN="$PWD/$c"; break; fi
				done
			fi
			test -n "$BIN" || { echo "no buildhost binary: build one first" >&2; exit 1; }
			# An APE is a polyglot the kernel cannot exec on its own: its header
			# is a shell script, and there is no APE binfmt handler here. A
			# native ELF is the opposite and must not go through a shell.
			bh() {
				if head -c 2 "$BIN" | grep -q MZ; then sh "$BIN" "$@"; else "$BIN" "$@"; fi
			}
			# The sandbox binds the working directory read-only; the test's own
			# output directory is the writable one.
			WORK="$(dirname "$LOG")"
			cd "$WORK"
			export BUILDHOST_DATA_DIR="$WORK/data"
			export BUILDHOST_DB_PATH="$WORK/data/buildhost.db"
			export BUILDHOST_LISTEN_ADDR="127.0.0.1:18081"
			export BUILDHOST_ADMIN_LISTEN_ADDR=""
			export BASE="http://localhost:18081"
			TOKEN="$(bh bootstrap --name dats | tail -1)"
			export TOKEN
			bh serve > "$LOG" 2>&1 &
			SERVER=$!
			# The server must not outlive the test: it holds the command's
			# stdout open, and dats reads that to the end.
			trap 'kill "$SERVER" 2>/dev/null || true' EXIT
			for _ in $(seq 60); do
				curl -fsS "$BASE/healthz" >/dev/null 2>&1 && break
				sleep 1
			done
			curl -fsS "$BASE/healthz" >/dev/null
			auth() { curl -fsS -H "Authorization: Bearer $TOKEN" "$@"; }
			# MZqFpD is the magic every Actually Portable Executable opens with.
			{ printf 'MZqFpD'; head -c 4096 /dev/urandom; } > ape.bin
			{ printf '\177ELF'; head -c 4096 /dev/urandom; } > plain.bin
			auth -X POST "$BASE/api/v1/projects" -H 'Content-Type: application/json' \
				-d '{"name":"ape-e2e","versioning":"auto"}' >/dev/null
			VERSION="$(auth -X POST "$BASE/api/v1/projects/ape-e2e/releases" \
				-H 'Content-Type: application/json' -d '{"git_branch":"master"}' | jq -r .version)"
			export VERSION

tests:
	- desc: an APE upload is one row carrying every platform it covers
	  inputs:
		env:
			LOG: "{outputs.server.log}"
	  cmd: |
		. {shared.serve.sh}
		auth -X PUT --data-binary @ape.bin -H 'X-Artifact-Filename: ape-e2e' \
			"$BASE/api/v1/projects/ape-e2e/releases/$VERSION/artifacts/ape?platforms=linux/amd64,darwin/arm64,windows/amd64" \
			-o artifact.json
		jq -e '.platforms | length == 3' artifact.json >/dev/null
		jq -e '.exe_format == "ape"' artifact.json >/dev/null
		auth -X POST "$BASE/api/v1/projects/ape-e2e/releases/$VERSION/publish" \
			-H 'Content-Type: application/json' -d '{}' >/dev/null
		echo "rows=$(auth "$BASE/api/v1/projects/ape-e2e/releases/$VERSION" | jq '.artifacts | length') artifacts"
	  outputs:
		stdout:
			- "rows=1 "

	- desc: every covered platform redirects to the same URL and the same bytes
	  inputs:
		env:
			LOG: "{outputs.server.log}"
	  cmd: |
		. {shared.serve.sh}
		auth -X PUT --data-binary @ape.bin -H 'X-Artifact-Filename: ape-e2e' \
			"$BASE/api/v1/projects/ape-e2e/releases/$VERSION/artifacts/ape?platforms=linux/amd64,darwin/arm64,windows/amd64" >/dev/null
		auth -X POST "$BASE/api/v1/projects/ape-e2e/releases/$VERSION/publish" \
			-H 'Content-Type: application/json' -d '{}' >/dev/null
		want="$(sha256sum ape.bin | awk '{print $1}')"
		first=""
		# macOS/aarch64 is the alias spelling: it must fold onto the same URL.
		for spec in linux/amd64 darwin/arm64 windows/amd64 macOS/aarch64; do
			os="${spec%%/*}"; arch="${spec##*/}"
			loc="$(curl -fsS -o /dev/null -w '%{redirect_url}' \
				"http://dl.localhost:18081/ape-e2e?os=$os&arch=$arch&v=$VERSION")"
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
	  inputs:
		env:
			LOG: "{outputs.server.log}"
	  cmd: |
		. {shared.serve.sh}
		auth -X PUT --data-binary @ape.bin -H 'X-Artifact-Filename: ape-e2e' \
			"$BASE/api/v1/projects/ape-e2e/releases/$VERSION/artifacts/ape?platforms=linux/amd64,darwin/arm64,windows/amd64" >/dev/null
		auth -X POST "$BASE/api/v1/projects/ape-e2e/releases/$VERSION/publish" \
			-H 'Content-Type: application/json' -d '{}' >/dev/null
		curl -fsS "$BASE/projects/ape-e2e/releases/$VERSION" > release.html
		grep -q 'badge badge-format' release.html
		grep -q 'APE: ' release.html
		grep -q 'linux/amd64, darwin/arm64, windows/amd64' release.html
		echo "raw-links=$(grep -c '>raw</a>' release.html) found"
	  outputs:
		stdout:
			- "raw-links=1 "

	- desc: a multi-platform claim without APE magic is refused, storing nothing
	  inputs:
		env:
			LOG: "{outputs.server.log}"
	  cmd: |
		. {shared.serve.sh}
		code="$(curl -sS -o reject.json -w '%{http_code}' -X PUT --data-binary @plain.bin \
			-H "Authorization: Bearer $TOKEN" \
			"$BASE/api/v1/projects/ape-e2e/releases/$VERSION/artifacts/ape?platforms=linux/amd64,darwin/arm64")"
		echo "code=$code returned"
		echo "rows=$(auth "$BASE/api/v1/projects/ape-e2e/releases/$VERSION" | jq '.artifacts | length') artifacts"
	  outputs:
		stdout:
			- "code=400 "
			- "rows=0 "
