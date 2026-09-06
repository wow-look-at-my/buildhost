# Every spelling of the entrypoint must reach the binary, and the image must
# carry no file that needs an ELF interpreter.
#
# A rolling update creates the replacement container from the OLD container's
# config, so the entrypoint it starts with is whatever the container it replaces
# recorded -- not the one this image declares. The shipped binary is an APE, and
# the kernel cannot exec one: its header is a shell script. So the image puts a
# shebang launcher on PATH and keeps the APE beside it, and every spelling has
# to land on that launcher.
#
# `docker compose -f docker-compose.ci.yml build` tags the image buildhost:ci.
# Starting the image with its own entrypoint is what container-healthcheck
# already does; these are the spellings it cannot reach. Needs the host's
# docker, so --no-sandbox.

shared:
	files:
		export.sh: |
			# Flatten the image once. dats runs tests concurrently, so a test
			# that consumed another test's output would race it.
			set -eu
			WORK="$(dirname "$ENV_FILE")"
			IMAGE="${BUILDHOST_IMAGE:-buildhost:ci}"
			docker image inspect "$IMAGE" >/dev/null
			# A registered APE binfmt handler makes the kernel run any APE
			# through a shell, so a bare exec of one succeeds and these tests
			# stop being able to fail. The handler is host-wide and containers
			# inherit it, so refuse to report a green nobody earned.
			for h in /proc/sys/fs/binfmt_misc/*; do
				case "${h##*/}" in status|register|'*') continue ;; esac
				if grep -qi '^magic 4d5a714670443d27$' "$h" 2>/dev/null &&
					grep -q '^enabled$' "$h" 2>/dev/null; then
					echo "an APE binfmt handler is registered at $h, so these tests would prove nothing; run this job without one" >&2
					exit 1
				fi
			done
			mkdir -p "$WORK/rootfs"
			cid="$(docker create "$IMAGE")"
			docker export "$cid" -o "$WORK/fs.tar"
			docker rm "$cid" >/dev/null
			tar -x -C "$WORK/rootfs" -f "$WORK/fs.tar"
			{
				echo "WORK='$WORK'"
				echo "IMAGE='$IMAGE'"
			} > "$ENV_FILE"

setup: env ENV_FILE={shared.env} sh {shared.export.sh}

tests:
	# The #240 case: the live container predated the APE and recorded the bare
	# name, so a bare exec of the APE is what the rolling update ran.
	- desc: a container that recorded the bare name on PATH still starts the server
	  cmd: |
		set -eu
		. {shared.env}
		docker run --rm --entrypoint buildhost "$IMAGE" version
	  outputs:
		stdout:
			- "buildhost"

	- desc: a container that recorded the absolute launcher path still starts the server
	  cmd: |
		set -eu
		. {shared.env}
		docker run --rm --entrypoint /usr/local/bin/buildhost "$IMAGE" version
	  outputs:
		stdout:
			- "buildhost"

	# The spelling the image used before the launcher existed.
	- desc: a container that recorded a shell in front of the path still starts the server
	  cmd: |
		set -eu
		. {shared.env}
		docker run --rm --entrypoint /bin/sh "$IMAGE" /usr/local/bin/buildhost version
	  outputs:
		stdout:
			- "buildhost"

	# The healthcheck runs the same way a container start does, so it needs the
	# launcher too. A healthcheck that names the APE marks every container
	# unhealthy, and a rolling update then rolls itself back.
	- desc: the image's entrypoint, command and healthcheck all name the launcher
	  cmd: |
		set -eu
		. {shared.env}
		docker image inspect "$IMAGE" --format 'entrypoint={{ json .Config.Entrypoint }} cmd={{ json .Config.Cmd }} healthcheck={{ json .Config.Healthcheck.Test }}'
	  outputs:
		stdout:
			- 'entrypoint=["/usr/local/bin/buildhost"]'
			- 'cmd=["serve"]'
			- 'healthcheck=["CMD","/usr/local/bin/buildhost","healthcheck"]'

	# The launcher runs /bin/sh, and /bin/sh is busybox. A busybox that names an
	# ELF interpreter cannot start on a base image with no /lib, and docker
	# reports that ENOENT against the entrypoint path rather than against the
	# shell.
	- desc: nothing the image ships asks for an ELF interpreter
	  cmd: |
		set -eu
		. {shared.env}
		: > "$WORK/elf.txt"
		find "$WORK/rootfs" -type f -print | while read -r f; do
			if file -b "$f" | grep -q '^ELF'; then
				printf '%s: %s\n' "${f#"$WORK/rootfs"}" "$(file -b "$f")" >> "$WORK/elf.txt"
			fi
		done
		cat "$WORK/elf.txt"
		if grep -q 'interpreter ' "$WORK/elf.txt"; then
			echo 'the image ships a dynamically linked executable, and the base image has no /lib to load its interpreter from' >&2
			exit 1
		fi
		# A vacuous pass is the failure mode of a check like this one: an empty
		# rootfs would also match no interpreter. The image ships busybox.
		found="$(wc -l < "$WORK/elf.txt")"
		test "$found" -ge 1 || { echo "expected at least 1 ELF file, found $found" >&2; exit 1; }
		echo "all-static elf=$found"
	  outputs:
		stdout:
			- "all-static elf="
