#!/usr/bin/env bash
# Log docker in to this buildhost's OCI registry with a freshly minted GitHub
# Actions OIDC token.
#
# The single implementation of that login, shared by buildhost-publish-docker
# (which logs in before the build and before the pull-back) and by the
# buildhost-docker-login action a consumer uses for a docker operation of its
# own. Consumers must never restate the audience, token endpoint or
# `oci.<domain>` rule; a second copy is a second thing to get wrong.
#
# The credential is short-lived, so call this immediately before the docker
# operation that needs it, not once at the top of a long job.
#
# Bash rather than `buildhost docker-login` (internal/ociclient/login.go, the
# same flow outside Actions) because this runs before the step that fetches the
# CLI, and a consumer would otherwise need a CLI just to log in.
#
# Usage: docker-login.sh <server-url>   (e.g. https://pazer.build)
set -euo pipefail

server="${1:?usage: docker-login.sh <server-url>}"

: "${ACTIONS_ID_TOKEN_REQUEST_URL:?OIDC unavailable. Add 'permissions: { id-token: write }' to this job.}"
: "${ACTIONS_ID_TOKEN_REQUEST_TOKEN:?OIDC unavailable. Add 'permissions: { id-token: write }' to this job.}"

# The OCI registry lives on the oci.<domain> subdomain; the server URL itself is
# the OIDC audience buildhost verifies the JWT against.
host="${server#*://}"; host="${host%%/*}"
registry="oci.${host}"

token="$(curl -fsS \
  -H "Authorization: Bearer ${ACTIONS_ID_TOKEN_REQUEST_TOKEN}" \
  "${ACTIONS_ID_TOKEN_REQUEST_URL}&audience=${server}" | jq -r '.value')"
if [ -z "${token}" ] || [ "${token}" = "null" ]; then
  echo "::error::could not obtain an OIDC token for ${server}" >&2
  exit 1
fi
echo "::add-mask::${token}"

printf '%s' "${token}" | docker login "${registry}" -u oidc --password-stdin
echo "logged in to ${registry}"
