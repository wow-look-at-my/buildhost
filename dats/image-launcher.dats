# The launcher must exec the binary, and nothing else.
#
# The kernel loads a file whose first bytes are ELF. An APE's are not, so its
# own header carries a trampoline that copies the ELF payload out and execs the
# copy. That copy needs a directory that is writable AND not noexec.
#
# The launcher used to hunt for one and export TMPDIR. That could never work:
# the trampoline hardcodes
#
#   c="/tmp/.ape-run-1-$(id -u 2>/dev/null || echo shared)/$k"
#
# and reads no TMPDIR. A deployment's noexec /tmp therefore killed every
# container at exit 126, against a path nobody chose, while the launcher
# announced a directory it had picked and changed nothing. The suite that
# covered the probe could not see this, because it put a shell script in place
# of the APE, and a shell script honours TMPDIR where the trampoline does not.
#
# The image now stages the ELF at build time, so nothing unpacks at run time and
# the launcher is one exec. These run against the real script with a stub in
# place of the binary, sandboxed by go-toolchain on every build.

shared:
	files:
		stub: |
			#!/bin/sh
			echo "STARTED args=$*"
		# The launcher hardcodes the binary's path, so the stub takes that path.
		install.sh: |
			set -eu
			mkdir -p "$WORK/usr/local/lib/buildhost"
			cp "$STUB" "$WORK/usr/local/lib/buildhost/buildhost"
			chmod 0755 "$WORK/usr/local/lib/buildhost/buildhost"
			sed -e "s|^real=.*|real=$WORK/usr/local/lib/buildhost/buildhost|" \
				scripts/image-launcher.sh > "$WORK/launcher.sh"
			chmod 0755 "$WORK/launcher.sh"

tests:
	- desc: the launcher starts the binary and passes its arguments through
	  cmd: |
		set -eu
		WORK="$(mktemp -d)"; export WORK
		STUB={shared.stub}; export STUB
		sh {shared.install.sh}
		sh "$WORK/launcher.sh" serve --flag
	  outputs:
		stdout:
			- "STARTED args=serve --flag"

	# TMPDIR named a directory for an unpack that no longer happens, and the
	# trampoline never read it even when it did.
	- desc: an unusable TMPDIR is irrelevant, because nothing unpacks
	  cmd: |
		set -eu
		WORK="$(mktemp -d)"; export WORK
		STUB={shared.stub}; export STUB
		sh {shared.install.sh}
		TMPDIR=/proc/definitely-not-writable sh "$WORK/launcher.sh" serve
	  outputs:
		stdout:
			- "STARTED args=serve"

	# The probe cost a mount scan and a copied binary per start, to choose a
	# directory nothing read. Its return was a container that died at exit 126.
	- desc: the launcher carries no unpack-directory machinery
	  cmd: |
		set -eu
		# Code lines only: the comment names these to say why they are gone.
		code="$(grep -v '^[[:space:]]*#' scripts/image-launcher.sh)"
		for pattern in TMPDIR /proc/mounts noexec /var/lib/ape; do
			if printf '%s\n' "$code" | grep -q -- "$pattern"; then
				echo "the launcher still carries $pattern, for an unpack that does not happen" >&2
				exit 1
			fi
		done
		echo no-probe
	  outputs:
		stdout:
			- "no-probe"

	# A shell in front of the path reads the binary as a script. That is what
	# the launcher did while the APE shipped, and an ELF is not a script.
	- desc: the launcher execs the binary rather than running it through a shell
	  cmd: |
		set -eu
		grep -q '^exec "\$real" "\$@"$' scripts/image-launcher.sh || {
			echo 'the launcher must exec the binary directly' >&2; exit 1; }
		if grep -qE 'exec +/bin/sh +"\$real"' scripts/image-launcher.sh; then
			echo 'the launcher still runs the binary through a shell' >&2; exit 1
		fi
		echo direct-exec
	  outputs:
		stdout:
			- "direct-exec"

	- desc: the launcher is a shebang script, which is what the kernel can exec
	  cmd: head -n1 scripts/image-launcher.sh
	  outputs:
		stdout:
			- "#!/bin/sh"
