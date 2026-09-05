# The image's ENTRYPOINT must never name the APE itself.
#
# This exists because the deployment sat for weeks on a version it could not
# start, while CI stayed green. The image entered through ["/bin/sh", <APE>],
# so it started only when something spelled the entrypoint that exact way. A
# rolling updater creates the new container from the OLD container's config,
# which carries the entrypoint resolved from the image that container was
# created from -- for one predating the APE, ["buildhost"]. That is a bare exec
# of an APE, which the kernel answers with ENOEXEC and docker reports as exit
# 126, on a loop.
#
# The fix is a shebang launcher on PATH, which the kernel CAN exec, in front of
# the APE. These assertions are about the Dockerfile, not a running container,
# for two reasons. The defect is a spelling in the Dockerfile. And a bare exec
# cannot be reproduced from a shell at all: when execve answers ENOEXEC, the
# shell runs the file as a script instead, so the broken form looks fine.
# Whether the launcher's target path is right is a runtime question, and
# container-healthcheck's `compose up --wait` answers it -- the launcher is the
# entrypoint, so a wrong path there is a container that does not start.

tests:
	- desc: the entrypoint and the healthcheck both name the launcher on PATH
	  cmd: grep -E '^(ENTRYPOINT|    CMD) \["/usr/local/bin/buildhost"' Dockerfile
	  outputs:
		stdout:
			- 'ENTRYPOINT ["/usr/local/bin/buildhost"]'
			- 'CMD ["/usr/local/bin/buildhost", "healthcheck"]'

	# The failing shape, in the two places it can come back.
	- desc: neither one puts a shell in front of a path
	  cmd: |
		set -eu
		if grep -nE '^(ENTRYPOINT|[[:space:]]*CMD) \["/bin/sh"' Dockerfile; then
			echo 'the entrypoint reads the APE through a shell; make the launcher the entrypoint' >&2
			exit 1
		fi
		echo no-shell-prefix
	  outputs:
		stdout:
			- "no-shell-prefix"

	- desc: what lands on PATH is the launcher script, and the APE lands elsewhere
	  cmd: grep -E '^COPY --chmod=755 (build/buildhost|scripts/image-launcher.sh) ' Dockerfile
	  outputs:
		stdout:
			- "COPY --chmod=755 build/buildhost /usr/local/lib/buildhost/buildhost"
			- "COPY --chmod=755 scripts/image-launcher.sh /usr/local/bin/buildhost"

	- desc: the launcher is a shebang script, which is what the kernel can exec
	  cmd: head -n1 scripts/image-launcher.sh
	  outputs:
		stdout:
			- "#!/bin/sh"

	- desc: the launcher starts the APE at the path the Dockerfile puts it
	  cmd: grep -E '^exec ' scripts/image-launcher.sh
	  outputs:
		stdout:
			- 'exec /bin/sh /usr/local/lib/buildhost/buildhost "$@"'
