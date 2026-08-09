#!/usr/bin/env bash
# Prints the buildx exporter's compression options for one optional level.
#
# Published layers are always zstd (see docs/formats/oci.md); the level is the
# only adjustable part. buildx reads an unparseable level as no level at all and
# builds at the default, so a typo would publish at a compression nobody chose
# and nothing would say so -- hence the explicit accept list and the hard exit.
set -euo pipefail

level="${1-}"
opts="compression=zstd,force-compression=true"

if [ -n "${level}" ]; then
	# Exactly 0-22 in canonical form. Spelling the range out beats an arithmetic
	# comparison, which accepts "0x9", reads "007" as 7, and errors on a number
	# too large for the shell rather than reporting it as the garbage it is.
	case "${level}" in
	[0-9] | 1[0-9] | 2[0-2]) ;;
	*)
		echo "::error::compression-level must be an integer 0-22 with no leading zeros or sign, got '${level}'" >&2
		exit 1
		;;
	esac
	opts="${opts},compression-level=${level}"
fi

printf '%s\n' "${opts}"
