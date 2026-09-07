# The launcher must hand the APE a directory it can actually exec from.
#
# execve loads a file whose first bytes are ELF, and an APE's are not, so the
# trampoline in the APE's own header copies the ELF payload out under TMPDIR and
# execs the copy. That copy needs a directory that is writable AND executable.
#
# A container's /tmp is commonly a noexec tmpfs, and compose offers the short
# form no exec option to turn that off. The write succeeds and the exec dies:
#
#   exec: line 29: /tmp/.ape-run-1-65532/153.2708.../buildhost: Permission denied
#
# The deployment spent hours restarting on exit 126 with that line, against a
# path the operator never chose and never sees again.
#
# The trampoline is a shell script, so it cannot reach memfd_create and
# execveat(AT_EMPTY_PATH) -- the one way Linux runs a program with no path at
# all, and therefore no mount to carry noexec. Choosing the directory is what is
# left, so the launcher probes candidates by RUNNING something in each: noexec
# belongs to the mount, not the file, so a stat under one still reports 0755.
#
# These run against the real script with a stub in place of the APE, sandboxed
# by go-toolchain on every build.

shared:
	files:
		stub: |
			#!/bin/sh
			echo "STARTED tmpdir=$TMPDIR args=$*"
		# The launcher hardcodes the APE's path, so the stub takes that path.
		install.sh: |
			set -eu
			mkdir -p "$WORK/usr/local/lib/buildhost"
			cp "$STUB" "$WORK/usr/local/lib/buildhost/buildhost"
			chmod 0755 "$WORK/usr/local/lib/buildhost/buildhost"
			sed -e "s|^real=.*|real=$WORK/usr/local/lib/buildhost/buildhost|" \
				scripts/image-launcher.sh > "$WORK/launcher.sh"
			chmod 0755 "$WORK/launcher.sh"

tests:
	- desc: a usable TMPDIR is honoured and passed through to the binary
	  cmd: |
		set -eu
		WORK="$(mktemp -d)"; export WORK
		STUB={shared.stub}; export STUB
		sh {shared.install.sh}
		mkdir -p "$WORK/mine"
		TMPDIR="$WORK/mine" BUILDHOST_DATA_DIR="$WORK/data" sh "$WORK/launcher.sh" serve
	  outputs:
		stdout:
			- "STARTED tmpdir="
			- "/mine args=serve"

	# The failure the deployment hit: TMPDIR names somewhere the copy cannot run.
	# The directory it lands on is not pinned here, because the candidate list
	# is what the next test covers. What matters is that it starts at all, and
	# never from the directory that failed.
	- desc: an unusable TMPDIR falls through instead of failing
	  cmd: |
		set -eu
		WORK="$(mktemp -d)"; export WORK
		STUB={shared.stub}; export STUB
		sh {shared.install.sh}
		out="$(TMPDIR=/proc/definitely-not-writable BUILDHOST_DATA_DIR="$WORK/data" sh "$WORK/launcher.sh" serve)"
		echo "$out"
		case "$out" in *definitely-not-writable*) echo 'it kept the TMPDIR it could not use' >&2; exit 1 ;; esac
	  outputs:
		stdout:
			- "STARTED tmpdir="
			- "args=serve"

	# /tmp is commonly a noexec tmpfs and the data directory is a VOLUME, so a
	# deployment can replace either. The directory the image itself ships is
	# tried before both.
	- desc: the image's own unpack directory is preferred over the data dir
	  cmd: |
		set -eu
		grep -n '/var/lib/ape' scripts/image-launcher.sh | head -n1
		awk '/^for candidate in/ {
			ape = index($0, "/var/lib/ape")
			data = index($0, "BUILDHOST_DATA_DIR")
			tmp = index($0, " /tmp ")
			if (ape > 0 && ape < data && ape < tmp) { print "ape-first"; exit 0 }
			print "the launcher tries a mountable directory first"; exit 1
		}' scripts/image-launcher.sh
	  outputs:
		stdout:
			- "ape-first"

	- desc: with every named directory unusable, the mount scan finds one
	  cmd: |
		set -eu
		WORK="$(mktemp -d)"; export WORK
		STUB={shared.stub}; export STUB
		sh {shared.install.sh}
		TMPDIR=/proc/nope BUILDHOST_DATA_DIR=/proc/nope sh "$WORK/launcher.sh" serve
	  outputs:
		stdout:
			- "STARTED tmpdir="

	# The probe has to RUN something. A directory that merely looks writable is
	# what let the old launcher hand /tmp to a trampoline that could not use it.
	# The probe copies a BINARY. A shebang script is read by its interpreter, so
	# running one can succeed where exec'ing a binary does not -- and a binary is
	# what the trampoline writes. A script here accepted /tmp on a deployment the
	# APE could not start in, and the launcher changed nothing.
	- desc: the probe execs a copied binary, not a shebang script
	  cmd: |
		set -eu
		grep -q 'cp /bin/sh "$probe"' scripts/image-launcher.sh || {
			echo 'the probe must copy a real binary: running a script proves nothing about exec' >&2; exit 1; }
		if grep -q "printf '#!/bin/sh" scripts/image-launcher.sh; then
			echo 'the probe still writes a shebang script' >&2; exit 1
		fi
		grep -q '126' scripts/image-launcher.sh || {
			echo 'the probe must judge whether exec itself succeeded' >&2; exit 1; }
		echo binary-probe
	  outputs:
		stdout:
			- "binary-probe"

	# busybox picks its applet from argv[0]. The image's /bin/sh IS busybox, so
	# a copy called anything else answers "applet not found" and exits 127. The
	# probe read that as a directory that cannot exec, and the container refused
	# every directory it was offered, the writable data volume included.
	- desc: the probe copy keeps the name sh
	  cmd: |
		set -eu
		grep -q 'probe="$dir/sh"' scripts/image-launcher.sh || {
			echo 'the probe copy must be named sh: busybox dispatches on argv[0]' >&2; exit 1; }
		echo named-sh
	  outputs:
		stdout:
			- "named-sh"

	# The trampoline stages its copy under APE_RUNDIR and ignores TMPDIR, which
	# every process inherits and which claims nothing about running a file. The
	# launcher probed the directory by exec'ing a binary in it, so it is the one
	# caller entitled to make that claim. Setting only TMPDIR left the copy in
	# /tmp, where the deployment mounts noexec, and the container died on 126
	# while the launcher's own log line named a directory that would have worked.
	- desc: the launcher exports the variable the trampoline reads
	  cmd: |
		set -eu
		grep -q 'export APE_RUNDIR' scripts/image-launcher.sh || {
			echo 'the launcher never exports APE_RUNDIR, so the APE still stages under /tmp' >&2; exit 1; }
		echo exports-ape-rundir
	  outputs:
		stdout:
			- "exports-ape-rundir"

	- desc: the launcher names the directory it chose
	  cmd: grep -c 'the APE unpacks into' scripts/image-launcher.sh
	  outputs:
		stdout:
			- "1"

	- desc: the candidate list asks the kernel what is mounted rw without noexec
	  cmd: |
		set -eu
		grep -q '/proc/mounts' scripts/image-launcher.sh || { echo 'the launcher never reads /proc/mounts' >&2; exit 1; }
		grep -q 'noexec' scripts/image-launcher.sh || { echo 'the launcher never excludes a noexec mount' >&2; exit 1; }
		echo scans-mounts
	  outputs:
		stdout:
			- "scans-mounts"
