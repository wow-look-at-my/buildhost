#!/usr/bin/env bash
# Prints the buildx exporter's compression options for one optional level.
set -euo pipefail

level="${1-}"
opts="compression=zstd,force-compression=true"

if [ -n "${level}" ]; then
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
