#!/usr/bin/env bash
# Log docker in to this buildhost's OCI registry with a freshly minted GitHub
# Actions OIDC token.
#
# This is INTERNAL to the buildhost repo on purpose. Authenticating to buildhost
# is this action's business, not its callers': a workflow that has to log in
# before it can build or pull is an action that did not finish its job, and the
# credential is short-lived, so "log in once at the top" is wrong anyway. Call
# this immediately before the docker operation that needs it.
#
# Bash rather than a buildhost CLI subcommand because this runs before the step
# that fetches the CLI, and because there is no supported docker-login CLI path
# for anything outside the official buildhost actions.
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
