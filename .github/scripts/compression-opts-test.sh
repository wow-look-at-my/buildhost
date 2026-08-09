#!/usr/bin/env bash
# Proves buildhost-publish-docker only accepts an integer level and rejects
# everything else. Every rejected case here is a value buildx would otherwise
# have silently ignored, publishing at a compression nobody chose.
set -uo pipefail

script="$(dirname "$0")/../actions/buildhost-publish-docker/compression-opts.sh"
base="compression=zstd,force-compression=true"
failures=0

# accepts <level> <expected stdout>
accepts() {
	local out status
	out="$(bash "${script}" "$1" 2>/dev/null)"
	status=$?
	if [ "${status}" -ne 0 ]; then
		echo "FAIL: level '$1' was rejected (exit ${status}); it is a valid level"
		failures=$((failures + 1))
	elif [ "${out}" != "$2" ]; then
		echo "FAIL: level '$1' produced '${out}', expected '$2'"
		failures=$((failures + 1))
	fi
}

# rejects <level>
rejects() {
	local out status
	out="$(bash "${script}" "$1" 2>&1)"
	status=$?
	if [ "${status}" -eq 0 ]; then
		echo "FAIL: level '$1' was accepted and produced '${out}'; it is not an integer 0-22"
		failures=$((failures + 1))
	elif [[ "${out}" != *"compression-level must be an integer 0-22"* ]]; then
		echo "FAIL: level '$1' was rejected without saying why: '${out}'"
		failures=$((failures + 1))
	fi
}

# Unset is the documented default: zstd at buildx's own level.
accepts "" "${base}"

for level in 0 1 9 10 19 20 21 22; do
	accepts "${level}" "${base},compression-level=${level}"
done

# Out of range, including the off-by-one either side of the zstd maximum.
rejects 23
rejects 99
rejects 100
rejects -1
rejects +9

# Not an integer at all. "true" and "default" are what someone reaches for after
# the compression and force-compression inputs stopped existing.
rejects true
rejects default
rejects zstd
rejects max
rejects 9.5
rejects 9,5
rejects 1e1
rejects 0x9
rejects " 9"
rejects "9 "
rejects "9;echo pwned"
rejects "\$(echo 9)"
rejects "٩"

# Leading zeros and signs are refused rather than guessed at: 022 could be
# decimal 22 or octal 18, and neither reading is worth publishing on.
rejects 007
rejects 022

if [ "${failures}" -ne 0 ]; then
	echo "compression-opts: ${failures} case(s) failed"
	exit 1
fi
echo "compression-opts: every level case behaved"
