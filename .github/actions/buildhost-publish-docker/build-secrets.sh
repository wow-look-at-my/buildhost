#!/usr/bin/env bash
# Prints the build-push-action secret-envs lines for the secret names in $1, one per line.
set -euo pipefail

names="${1-}"
while IFS= read -r name; do
	name="${name//[[:space:]]/}"
	[ -n "${name}" ] || continue
	if ! [[ "${name}" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
		echo "::error::build-secrets: '${name}' is not a secret name" >&2
		exit 1
	fi
	if [ -z "${!name-}" ]; then
		echo "::error::build-secrets: secret-server gave this repository no ${name}, so the build cannot mount it" >&2
		exit 1
	fi
	echo "${name}=${name}"
done <<<"${names}"
