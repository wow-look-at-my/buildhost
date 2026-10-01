# buildhost-publish-docker accepts an integer compression level and nothing
# else. Every rejected case here is a value buildx would otherwise have
# silently ignored, publishing at a compression nobody chose.
#
# see docs/formats/oci.md

tests:
	# Unset is the documented default: zstd at buildx's own level.
	- desc: no level publishes zstd at the default level
	  cmd: bash .github/actions/buildhost-publish-docker/compression-opts.sh
	  outputs:
		stdout:
			- "compression=zstd,force-compression=true"
		!stdout:
			- "compression-level"

	- desc: every integer in 0-22 becomes a compression-level option
	  cmd: |
		set -eu
		for level in 0 1 9 10 19 20 21 22; do
			got="$(bash .github/actions/buildhost-publish-docker/compression-opts.sh "$level")"
			want="compression=zstd,force-compression=true,compression-level=$level"
			test "$got" = "$want" || { echo "level $level produced $got, want $want" >&2; exit 1; }
		done
		echo "levels-accepted"
	  outputs:
		stdout:
			- "levels-accepted"

	# Out of range, including the off-by-one either side of the zstd maximum.
	# "true" and "default" are what someone reaches for after the compression
	# and force-compression inputs stopped existing. Leading zeros and signs
	# are refused rather than guessed at: 022 could be decimal 22 or octal 18,
	# and neither reading is worth publishing on.
	- desc: anything that is not a canonical integer 0-22 is refused, with the reason
	  cmd: |
		set -eu
		reject() {
			if out="$(bash .github/actions/buildhost-publish-docker/compression-opts.sh "$1" 2>&1)"; then
				echo "level '$1' was accepted and produced $out" >&2; exit 1
			fi
			case "$out" in
				*"compression-level must be an integer 0-22"*) ;;
				*) echo "level '$1' was rejected without saying why: $out" >&2; exit 1 ;;
			esac
		}
		for bad in 23 99 100 -1 +9 true default zstd max 9.5 9,5 1e1 0x9 007 022; do
			reject "$bad"
		done
		reject " 9"
		reject "9 "
		reject "9;echo pwned"
		reject '$(echo 9)'
		reject "٩"
		echo "non-integers-refused"
	  outputs:
		stdout:
			- "non-integers-refused"
		!stdout:
			- "pwned"
