#!/usr/bin/env bash
# Log docker in to this buildhost's OCI registry with a freshly minted GitHub Actions OIDC token.
set -euo pipefail

server="${1:?usage: docker-login.sh <server-url>}"

: "${ACTIONS_ID_TOKEN_REQUEST_URL:?OIDC unavailable. Add 'permissions: { id-token: write }' to this job.}"
: "${ACTIONS_ID_TOKEN_REQUEST_TOKEN:?OIDC unavailable. Add 'permissions: { id-token: write }' to this job.}"

# The OCI registry lives on the oci.<domain> subdomain.
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
