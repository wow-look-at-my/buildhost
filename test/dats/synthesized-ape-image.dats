# An image buildhost synthesizes from an APE must RUN.
#
# The kernel cannot exec an APE: the file's header is a shell script, and no
# binfmt handler is registered. So the synthesizer writes the ELF the APE's own
# trampoline would have staged, keeps it under /usr/local/lib, and puts a
# shebang launcher at the entrypoint path. The base layer carries the shell that
# launcher needs. Get any part of that wrong and every container from the image
# dies at exec, reporting "no such file or directory" against an entrypoint that
# is present.
#
# The trampoline stages into a hardcoded /tmp that TMPDIR does not move, so an
# image that shipped the APE itself dies on a noexec /tmp instead.
#
# The sibling suite synthesizes from a plain ELF, which exercises none of this.
# The payload here is the repo's own fat APE, so the test runs a real one.
#
# The workflow installs crane, maps the OCI host in /etc/hosts, turns on the
# containerd image store and passes $BUILDHOST_BIN. Needs curl, jq, crane and
# docker, so --no-sandbox.
#
# see docs/formats/oci.md

shared:
	files:
		start.sh: |
			# Publish the APE and leave the server running.
			set -eu
			WORK="$(dirname "$ENV_FILE")"
			PORT="${PORT:-8089}"
			HOST="oci.buildhost.test"
			BASE="http://127.0.0.1:$PORT"
			PROJECT="ape-image"
			test -x "$BUILDHOST_BIN" || { echo "not executable: $BUILDHOST_BIN" >&2; exit 1; }
			# The payload must actually be an APE, or this suite proves nothing.
			head -c 8 "$BUILDHOST_BIN" | grep -q "MZqFpD='" || {
				echo "BUILDHOST_BIN is not an APE, so this suite would test the ELF path" >&2; exit 1; }
			# A registered APE binfmt handler makes the kernel run any APE
			# through a shell, so an image with no shell layer would still
			# start and this suite would stop being able to fail.
			for h in /proc/sys/fs/binfmt_misc/*; do
				case "${h##*/}" in status|register|'*') continue ;; esac
				if grep -qi '^magic 4d5a714670443d27$' "$h" 2>/dev/null &&
					grep -q '^enabled$' "$h" 2>/dev/null; then
					echo "an APE binfmt handler is registered at $h, so these tests would prove nothing; run this job without one" >&2
					exit 1
				fi
			done
			RUN="sh $BUILDHOST_BIN"
			BUILDHOST_DATA_DIR="$WORK/data"; export BUILDHOST_DATA_DIR
			BUILDHOST_DB_PATH="$WORK/data/buildhost.db"; export BUILDHOST_DB_PATH
			BUILDHOST_LISTEN_ADDR=":$PORT"; export BUILDHOST_LISTEN_ADDR
			BUILDHOST_ADMIN_LISTEN_ADDR=""; export BUILDHOST_ADMIN_LISTEN_ADDR
			TOKEN="$($RUN bootstrap --name ape-e2e | tail -n1)"
			test -n "$TOKEN" || { echo "no token from bootstrap" >&2; exit 1; }
			setsid $RUN serve > "$WORK/server.log" 2>&1 &
			echo "$!" > "$WORK/server.pid"
			started=""
			# A hook gets 30s in total, so a one-second poll spends the whole
			# budget waiting and reports a timeout instead of the server log.
			for _ in $(seq 50); do
				if curl -fsS "$BASE/healthz" >/dev/null 2>&1; then started=yes; break; fi
				sleep 0.2
			done
			test -n "$started" || { echo "server did not become healthy:" >&2; cat "$WORK/server.log" >&2; exit 1; }
			auth() { curl -fsS -H "Authorization: Bearer $TOKEN" "$@"; }
			auth -X POST "$BASE/api/v1/projects" -H 'Content-Type: application/json' \
				-d "{\"name\":\"$PROJECT\",\"versioning\":\"auto\",\"is_private\":false}" >/dev/null
			VERSION="$(auth -X POST "$BASE/api/v1/projects/$PROJECT/releases" \
				-H 'Content-Type: application/json' -d '{"git_branch":"master"}' | jq -r .version)"
			auth -X PUT --data-binary "@$BUILDHOST_BIN" -H "X-Artifact-Filename: $PROJECT" \
				"$BASE/api/v1/projects/$PROJECT/releases/$VERSION/artifacts/linux/amd64?kind=binary" >/dev/null
			auth -X POST "$BASE/api/v1/projects/$PROJECT/releases/$VERSION/publish" >/dev/null
			# Flatten the image here rather than in a test: dats runs tests
			# concurrently, so a test that consumed another test's output
			# would race it.
			REF="$HOST:$PORT/$PROJECT:latest"
			mkdir -p "$WORK/rootfs"
			crane export --insecure "$REF" - | tar -x -C "$WORK/rootfs"
			{
				echo "WORK='$WORK'"
				echo "REF='$REF'"
				echo "PROJECT='$PROJECT'"
			} > "$ENV_FILE"

setup: env ENV_FILE={shared.env} sh {shared.start.sh}
teardown: sh -c '. {shared.env}; kill -- "-$(cat "$WORK/server.pid")" 2>/dev/null; true'

tests:
	# A bare APE path as the entrypoint is the defect: it is what a synthesis
	# without the shell layer produces, and no container from such an image
	# ever starts.
	- desc: the entrypoint names a launcher, not the binary, over the two layers
	  cmd: |
		set -eu
		. {shared.env}
		# A test's working directory is the repo root, so write under $WORK.
		crane config --insecure "$REF" > "$WORK/config.json"
		cat "$WORK/config.json"
		echo "diff_ids=$(jq '.rootfs.diff_ids | length' "$WORK/config.json")"
		echo "entrypoint=$(jq -c '.config.Entrypoint' "$WORK/config.json")"
	  outputs:
		stdout:
			- "diff_ids=2"
			- 'entrypoint=["/ape-image"]'

	- desc: the base layer lands a real shell, and it needs no ELF interpreter
	  cmd: |
		set -eu
		. {shared.env}
		test -x "$WORK/rootfs/bin/sh" || { echo "no /bin/sh in the synthesized image" >&2; exit 1; }
		test -x "$WORK/rootfs/usr/local/lib/$PROJECT/$PROJECT" || { echo "the binary is not under /usr/local/lib" >&2; exit 1; }
		file -b "$WORK/rootfs/bin/busybox"
		if file -b "$WORK/rootfs/bin/busybox" | grep -q 'interpreter '; then
			echo 'the shell layer ships a dynamically linked busybox, and the image has no /lib for its interpreter' >&2
			exit 1
		fi
		echo "shell-static"
	  outputs:
		stdout:
			- "shell-static"

	# The image must carry the staged ELF, not the APE. An APE here starts only
	# where /tmp is writable and executable, which a deployment's /tmp is not.
	- desc: the shipped binary is an ELF the kernel loads with no staging
	  cmd: |
		set -eu
		. {shared.env}
		bin="$WORK/rootfs/usr/local/lib/$PROJECT/$PROJECT"
		if head -c 8 "$bin" | grep -q "MZqFpD='"; then
			echo 'the image ships the APE itself, so every start stages a copy under a hardcoded /tmp' >&2
			exit 1
		fi
		head -c 4 "$bin" | grep -q 'ELF' || { echo 'the shipped binary is neither an APE nor an ELF' >&2; exit 1; }
		echo "staged-elf"
	  outputs:
		stdout:
			- "staged-elf"

	# The failure this whole layout exists for: a noexec /tmp is what a
	# deployment gives a container, and the APE trampoline stages there.
	- desc: the container starts with /tmp mounted noexec
	  cmd: |
		set -eu
		. {shared.env}
		docker image inspect "$REF" >/dev/null 2>&1 || docker pull "$REF" >/dev/null
		docker run --rm --tmpfs /tmp:noexec,nosuid,nodev "$REF" version | head -n1
	  outputs:
		stdout:
			- "buildhost"

	# The whole point: an image nobody ever started is what shipped the defect
	# this suite exists for.
	- desc: docker pulls the image and the APE inside it runs
	  cmd: |
		set -eu
		. {shared.env}
		docker image rm "$REF" >/dev/null 2>&1 || true
		docker pull "$REF"
		docker image inspect "$REF" --format '{{ .Os }}/{{ .Architecture }}'
		docker run --rm "$REF" version
	  outputs:
		stdout:
			- "linux/amd64"
			- "buildhost"

	# A rolling updater creates the replacement from the config of the container
	# it replaces, so the entrypoint that reaches this image is whatever the OLD
	# one recorded. Every spelling a synthesized image ever gave must therefore
	# still start, or the deployment is wedged on the version it already runs.
	- desc: an entrypoint an older container recorded still starts the server
	  cmd: |
		set -eu
		. {shared.env}
		docker image inspect "$REF" >/dev/null 2>&1 || docker pull "$REF" >/dev/null
		# The absolute path every image before the launcher carried.
		docker run --rm --entrypoint "/$PROJECT" "$REF" version | head -n1
		# The bare name, found on PATH.
		docker run --rm --entrypoint "$PROJECT" "$REF" version | head -n1
		# A shell in front of the path, which the shell-layer images used.
		docker run --rm --entrypoint /bin/sh "$REF" "/$PROJECT" version | head -n1
		echo "every-spelling-starts"
	  outputs:
		stdout:
			- "every-spelling-starts"
