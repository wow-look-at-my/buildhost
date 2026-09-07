#!/bin/sh
# The image installs this at /usr/local/bin/buildhost, and the APE beside it
# under /usr/local/lib. The kernel cannot exec an APE: the file's header is a
# shell script, and the image registers no binfmt handler. A shebang script IS
# execable, so every spelling of the entrypoint reaches the binary -- the
# image's own, a "buildhost" found on PATH, and an absolute path.
#
# execve loads a file whose first bytes are ELF, and an APE's are not, so the
# trampoline copies the ELF payload out under TMPDIR and execs the copy. That
# copy needs a directory that is writable AND executable. A container's /tmp is
# commonly a noexec tmpfs, where the write succeeds and the exec dies against a
# path nobody recognises:
#
#   exec: line 29: /tmp/.ape-run-1-65532/153.2708.../buildhost: Permission denied
#
# So find a directory that passes both tests before handing over.
set -eu

: "${BUILDHOST_DATA_DIR:=/var/lib/buildhost}"
real=/usr/local/lib/buildhost/buildhost

# usable reports whether dir can hold a program and run it.
#
# It RUNS something rather than read a permission: noexec belongs to the mount,
# not the file, so every stat on a file under a noexec mount still says 0755.
usable() {
	[ -n "${1:-}" ] || return 1
	mkdir -p "$1" 2>/dev/null || return 1
	probe="$1/.ape-exec-probe.$$"
	printf '#!/bin/sh\nexit 0\n' > "$probe" 2>/dev/null || return 1
	if chmod 0700 "$probe" 2>/dev/null && "$probe" 2>/dev/null; then
		rm -f "$probe"
		return 0
	fi
	rm -f "$probe" 2>/dev/null || true
	return 1
}

# writableMounts lists what the kernel says is mounted read-write without
# noexec, nearest-fit first. A fixed list of guesses goes stale against whatever
# a deployment actually mounts; this asks.
writableMounts() {
	[ -r /proc/mounts ] || return 0
	awk '{
		opts = "," $4 ","
		if (opts !~ /,rw,/) next
		if (opts ~ /,noexec,/) next
		print $2
	}' /proc/mounts 2>/dev/null
}

# The data volume leads: a deployment that cannot write there is already broken,
# and a volume carries no noexec of its own. An operator's TMPDIR is honoured
# first, then the usual temporary directories, then whatever is left mounted.
for candidate in "${TMPDIR:-}" "$BUILDHOST_DATA_DIR/.ape" /tmp /var/tmp /dev/shm $(writableMounts); do
	case "$candidate" in
	"" | /proc* | /sys* | /dev/pts*) continue ;;
	esac
	if usable "$candidate"; then
		TMPDIR="$candidate"
		export TMPDIR
		exec /bin/sh "$real" "$@"
	fi
done

# Nothing worked, so say what was tried and what the kernel offered. The
# trampoline's own message names a path under a directory the operator never
# chose, and never mentions the mount that refused it.
echo "buildhost: found no writable, executable directory for the APE to unpack into." >&2
echo "buildhost: the binary unpacks itself and execs the copy, so one must be writable AND not mounted noexec." >&2
echo "buildhost: tried TMPDIR=${TMPDIR:-<unset>}, $BUILDHOST_DATA_DIR/.ape, /tmp, /var/tmp, /dev/shm and every rw mount below." >&2
echo "buildhost: a container's /tmp is often a noexec tmpfs. Set TMPDIR to a path on a volume, which carries no noexec of its own." >&2
if [ -r /proc/mounts ]; then
	echo "buildhost: mounts:" >&2
	awk '{ print "  " $2 "  " $3 "  " $4 }' /proc/mounts >&2
fi
exit 126
