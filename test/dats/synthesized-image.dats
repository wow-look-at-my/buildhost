# An image buildhost SYNTHESIZES from a plain binary must be a valid, pullable
# container image whose baked-in CA bundle works for real outbound HTTPS.
#
# Two clients, on purpose. crane (go-containerregistry) is daemon-free, so this
# runs the same way locally and on a runner. Docker is exercised as well,
# because "pullable" is a claim about the client people actually use, and
# buildhost's layers are zstd -- which Docker reads only through the containerd
# image store. crane alone proved the half that was never in doubt.
#
# The workflow builds the netcheck entrypoint, installs crane, maps the OCI
# host in /etc/hosts and turns on the containerd image store; $NETCHECK_BIN and
# $BUILDHOST_BIN come from it. Needs curl, crane and docker, so --no-sandbox.
#
# see docs/formats/oci.md

shared:
	files:
		start.sh: |
			# Publish the entrypoint binary and leave the server running.
			set -eu
			WORK="$(dirname "$ENV_FILE")"
			PORT="${PORT:-8088}"
			HOST="oci.buildhost.test"
			BASE="http://127.0.0.1:$PORT"
			test -x "$BUILDHOST_BIN" || { echo "not executable: $BUILDHOST_BIN" >&2; exit 1; }
			test -x "$NETCHECK_BIN" || { echo "not executable: $NETCHECK_BIN" >&2; exit 1; }
			# An APE cannot be exec'd: its header is a shell script, and
			# nothing here registers an APE binfmt handler.
			if head -c 2 "$BUILDHOST_BIN" | grep -q MZ; then RUN="sh $BUILDHOST_BIN"; else RUN="$BUILDHOST_BIN"; fi
			# DBPath is independent of DataDir. The admin server is off so this
			# takes one port, not two.
			BUILDHOST_DATA_DIR="$WORK/data"; export BUILDHOST_DATA_DIR
			BUILDHOST_DB_PATH="$WORK/data/buildhost.db"; export BUILDHOST_DB_PATH
			BUILDHOST_LISTEN_ADDR=":$PORT"; export BUILDHOST_LISTEN_ADDR
			BUILDHOST_ADMIN_LISTEN_ADDR=""; export BUILDHOST_ADMIN_LISTEN_ADDR
			TOKEN="$($RUN bootstrap --name e2e | tail -n1)"
			test -n "$TOKEN" || { echo "no token from bootstrap" >&2; exit 1; }
			# setsid: the server leads its own process group, so teardown takes
			# the whole tree down.
			setsid $RUN serve > "$WORK/server.log" 2>&1 &
			echo "$!" > "$WORK/server.pid"
			started=""
			for _ in $(seq 50); do
				if curl -fsS "$BASE/healthz" >/dev/null 2>&1; then started=yes; break; fi
				sleep 1
			done
			test -n "$started" || { echo "server did not become healthy:" >&2; cat "$WORK/server.log" >&2; exit 1; }
			auth() { curl -fsS -H "Authorization: Bearer $TOKEN" "$@"; }
			auth -X POST "$BASE/api/v1/projects" -H 'Content-Type: application/json' \
				-d '{"name":"netcheck","versioning":"auto","is_private":false}' >/dev/null
			VERSION="$(auth -X POST "$BASE/api/v1/projects/netcheck/releases" \
				-H 'Content-Type: application/json' -d '{"git_branch":"master"}' | jq -r .version)"
			auth -X PUT --data-binary "@$NETCHECK_BIN" \
				"$BASE/api/v1/projects/netcheck/releases/$VERSION/artifacts/linux/amd64?kind=binary" >/dev/null
			auth -X POST "$BASE/api/v1/projects/netcheck/releases/$VERSION/publish" >/dev/null
			# Flatten the image here rather than in a test: dats runs tests
			# concurrently, so a test that consumed another test's output
			# would race it.
			REF="$HOST:$PORT/netcheck:latest"
			mkdir -p "$WORK/rootfs"
			crane export --insecure "$REF" - | tar -x -C "$WORK/rootfs"
			{
				echo "WORK='$WORK'"
				echo "REF='$REF'"
			} > "$ENV_FILE"

setup: env ENV_FILE={shared.env} sh {shared.start.sh}
teardown: sh -c '. {shared.env}; kill -- "-$(cat "$WORK/server.pid")" 2>/dev/null; true'

tests:
	- desc: the synthesized image config names the entrypoint, the CA bundle and both layers
	  cmd: |
		set -eu
		. {shared.env}
		crane config --insecure "$REF" > config.json
		cat config.json
		grep -q '"Entrypoint":\["/netcheck"\]' config.json || { echo "entrypoint is not /netcheck" >&2; exit 1; }
		grep -q 'SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt' config.json || {
			echo "SSL_CERT_FILE env missing" >&2; exit 1; }
		# The essentials base layer plus the per-binary layer, in order.
		echo "diff_ids=$(jq '.rootfs.diff_ids | length' config.json)"
	  outputs:
		stdout:
			- "diff_ids=2"

	- desc: the flattened rootfs carries the CA bundle, the nonroot user and the binary
	  cmd: |
		set -eu
		. {shared.env}
		test -s "$WORK/rootfs/etc/ssl/certs/ca-certificates.crt" || { echo "CA bundle missing or empty" >&2; exit 1; }
		grep -q '^nonroot:x:65532:65532:' "$WORK/rootfs/etc/passwd" || { echo "nonroot user missing" >&2; exit 1; }
		test -x "$WORK/rootfs/netcheck" || { echo "entrypoint binary missing" >&2; exit 1; }
		echo "rootfs-complete"
	  outputs:
		stdout:
			- "rootfs-complete"

	# The whole point of the essentials layer: a live HTTPS request validated
	# ONLY against the bundle the image itself carries.
	- desc: the entrypoint validates a live HTTPS request with the image's own CA bundle
	  cmd: |
		set -eu
		. {shared.env}
		SSL_CERT_FILE="$WORK/rootfs/etc/ssl/certs/ca-certificates.crt" "$WORK/rootfs/netcheck"
		echo "https-ok"
	  outputs:
		stdout:
			- "https-ok"

	# zstd layers need the containerd image store. On the classic graphdriver a
	# consumer gets an unreadable-manifest error at pull time rather than
	# anything actionable, so this is the half that breaks real users.
	- desc: docker pulls and runs the image
	  cmd: |
		set -eu
		. {shared.env}
		docker image rm "$REF" >/dev/null 2>&1 || true
		docker pull "$REF"
		docker image inspect "$REF" --format '{{ .Os }}/{{ .Architecture }}'
		docker run --rm "$REF"
	  outputs:
		stdout:
			- "linux/amd64"
