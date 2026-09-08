# The SHIPPED IMAGE must work as a registry: push an image in, pull an image
# out, and run what came out.
#
# Every other OCI check stops short of this. The unit tests drive handlers
# through httptest and never start a container. synthesized-image.dats runs the
# buildhost BINARY on the host and pulls a synthesized image from it. This suite
# is the only one where a real docker daemon talks to buildhost running as the
# container it ships, over the wire, in both directions.
#
# The two directions are different code paths and both are covered here:
#
#   push  -- docker uploads blobs and PUTs a manifest, and buildhost records a
#            kind=docker release. Pulling that back proves the bytes round-trip.
#   pull  -- a release published through the REST API carries no image at all.
#            buildhost SYNTHESIZES one per platform at pull time. That path is
#            where a dropped platform, an unlinked layer blob or a binary the
#            kernel cannot exec turns into a pull that fails at the client.
#
# The payload for the synthesized side is the repo's own APE, so the pulled
# image exercises the staged-ELF path and the shell in the base layer. Running
# it is the assertion: an image nobody starts is what shipped the defect this
# suite exists for.
#
# The workflow starts the image with compose, maps the OCI host in /etc/hosts,
# marks it insecure, turns on the containerd image store (buildhost's layers are
# zstd) and passes $BUILDHOST_BIN and $MARKER_BIN. Needs curl, jq and the docker
# CLI, so a workflow runs it --no-sandbox.
#
# see docs/formats/oci.md

shared:
	files:
		start.sh: |
			# Log docker in, push an image, and publish a binary for the pull
			# side. Writes $ENV_FILE.
			set -eu
			WORK="$(dirname "$ENV_FILE")"
			REGISTRY="oci.buildhost.test:8080"
			BASE="http://localhost:8080"
			PUSHED="pushed-image"
			SYNTH="synth-image"
			test -x "$BUILDHOST_BIN" || { echo "not executable: $BUILDHOST_BIN" >&2; exit 1; }
			test -x "$MARKER_BIN" || { echo "not executable: $MARKER_BIN" >&2; exit 1; }
			# The payload must be an APE, or the synthesized side tests the ELF
			# path and the staging this suite exists to cover never runs.
			head -c 8 "$BUILDHOST_BIN" | grep -q "MZqFpD='" || {
				echo "BUILDHOST_BIN is not an APE, so the pull side would test the plain-ELF path" >&2; exit 1; }
			# A registered APE binfmt handler makes the kernel run any APE
			# through a shell, so an image whose binary was never staged would
			# still start and this suite would stop being able to fail.
			for h in /proc/sys/fs/binfmt_misc/*; do
				case "${h##*/}" in status|register|'*') continue ;; esac
				if grep -qi '^magic 4d5a714670443d27$' "$h" 2>/dev/null &&
					grep -q '^enabled$' "$h" 2>/dev/null; then
					echo "an APE binfmt handler is registered at $h, so these tests would prove nothing; run this job without one" >&2
					exit 1
				fi
			done

			TOKEN="$(docker compose -f "$REPO/docker-compose.ci.yml" exec -T buildhost \
				/usr/local/bin/buildhost bootstrap --name oci-roundtrip | tail -1 | tr -d '\r')"
			test -n "$TOKEN" || { echo "no token from bootstrap inside the container" >&2; exit 1; }
			auth() { curl -fsS -H "Authorization: Bearer $TOKEN" "$@"; }

			printf '%s' "$TOKEN" | docker login "$REGISTRY" -u buildhost --password-stdin

			# PUSH SIDE. A scratch image around a static binary: the whole image
			# is bytes this suite controls, so a mismatch on the way back out is
			# buildhost's and not a base image's.
			mkdir -p "$WORK/ctx"
			cp "$MARKER_BIN" "$WORK/ctx/marker"
			chmod 0755 "$WORK/ctx/marker"
			printf 'FROM scratch\nCOPY marker /marker\nENTRYPOINT ["/marker"]\n' > "$WORK/ctx/Dockerfile"
			auth -X POST "$BASE/api/v1/projects" -H 'Content-Type: application/json' \
				-d "{\"name\":\"$PUSHED\",\"versioning\":\"auto\",\"is_private\":false}" >/dev/null
			docker build -t "$REGISTRY/$PUSHED:v1" "$WORK/ctx"
			docker push "$REGISTRY/$PUSHED:v1"

			# PULL SIDE. No image is uploaded: buildhost synthesizes one from
			# the published binary when a client asks for it.
			auth -X POST "$BASE/api/v1/projects" -H 'Content-Type: application/json' \
				-d "{\"name\":\"$SYNTH\",\"versioning\":\"auto\",\"is_private\":false}" >/dev/null
			VERSION="$(auth -X POST "$BASE/api/v1/projects/$SYNTH/releases" \
				-H 'Content-Type: application/json' -d '{"git_branch":"master"}' | jq -r .version)"
			auth -X PUT --data-binary "@$BUILDHOST_BIN" -H "X-Artifact-Filename: $SYNTH" \
				"$BASE/api/v1/projects/$SYNTH/releases/$VERSION/artifacts/linux/amd64?kind=binary" >/dev/null
			auth -X POST "$BASE/api/v1/projects/$SYNTH/releases/$VERSION/publish" >/dev/null

			{
				echo "WORK='$WORK'"
				echo "BASE='$BASE'"
				echo "REGISTRY='$REGISTRY'"
				echo "PUSHED='$PUSHED'"
				echo "SYNTH='$SYNTH'"
				echo "TOKEN='$TOKEN'"
			} > "$ENV_FILE"

setup: env ENV_FILE={shared.env} sh {shared.start.sh}

tests:
	# The push landed as a release the API reports, not just as blobs in a
	# directory: a push the rest of buildhost cannot see is not stored.
	- desc: the pushed image is a docker release the API reports
	  cmd: |
		set -eu
		. {shared.env}
		curl -fsS -H "Authorization: Bearer $TOKEN" \
			"$BASE/api/v1/projects/$PUSHED/releases/latest" | jq -r '.artifacts[].kind'
	  outputs:
		stdout:
			- "docker"

	# The bytes have to survive the round trip. Removing the local image first
	# is what makes the run below read from buildhost instead of the daemon's
	# own cache, which is how a broken pull passes unnoticed.
	- desc: the pushed image pulls back out and runs
	  cmd: |
		set -eu
		. {shared.env}
		docker image rm "$REGISTRY/$PUSHED:v1" >/dev/null 2>&1 || true
		docker pull "$REGISTRY/$PUSHED:v1"
		docker run --rm "$REGISTRY/$PUSHED:v1"
	  outputs:
		stdout:
			- "MARKER-OK"

	# The defect this suite was written for: the index served for an APE
	# omitted linux/amd64, the artifact's own canonical platform and the only
	# one anything can run. docker reported "no matching manifest for
	# linux/amd64 in the manifest list entries" and the cause reached nobody.
	- desc: the synthesized image advertises the platform the host runs
	  cmd: |
		set -eu
		. {shared.env}
		docker manifest inspect --insecure "$REGISTRY/$SYNTH:latest" \
			| jq -r 'if .manifests then .manifests[] | "\(.platform.os)/\(.platform.architecture)" else "\(.config.digest | "single")" end' \
			| sort
		echo "index-read"
	  outputs:
		stdout:
			- "linux/amd64"
			- "index-read"
		!stdout:
			- "darwin/"
			- "windows/"

	# The whole point: an image nobody starts is what shipped the defect.
	- desc: the synthesized image pulls and the binary inside it runs
	  cmd: |
		set -eu
		. {shared.env}
		docker image rm "$REGISTRY/$SYNTH:latest" >/dev/null 2>&1 || true
		docker pull "$REGISTRY/$SYNTH:latest"
		docker image inspect "$REGISTRY/$SYNTH:latest" --format '{{ .Os }}/{{ .Architecture }}'
		docker run --rm "$REGISTRY/$SYNTH:latest" version
	  outputs:
		stdout:
			- "linux/amd64"
			- "buildhost"

	# A deployment gives a container a noexec /tmp. The APE trampoline stages
	# its copy under a hardcoded /tmp path that no variable moves, so an image
	# shipping the APE itself dies here on exit 126 against a path nobody chose.
	- desc: the synthesized image starts with /tmp mounted noexec
	  cmd: |
		set -eu
		. {shared.env}
		docker image inspect "$REGISTRY/$SYNTH:latest" >/dev/null 2>&1 || docker pull "$REGISTRY/$SYNTH:latest" >/dev/null
		docker run --rm --tmpfs /tmp:noexec,nosuid,nodev "$REGISTRY/$SYNTH:latest" version | head -n1
	  outputs:
		stdout:
			- "buildhost"
