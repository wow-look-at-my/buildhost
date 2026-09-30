#!/usr/bin/env bash
# Fetch the public CA bundle that gets embedded into the buildhost binary (and from
# there into every OCI image buildhost synthesizes from a plain binary).
#
# Run this before `go-toolchain` / `go build` locally; CI runs it in the build job.
set -euo pipefail

DEST="${1:-internal/repackage/cacerts/ca-certificates.crt}"
URL="${CACERT_URL:-https://curl.se/ca/cacert.pem}"   # Mozilla CA bundle, as published by curl

mkdir -p "$(dirname "$DEST")"
curl -fsSL --retry 3 --proto '=https' --tlsv1.2 -o "$DEST" "$URL"
echo "fetched CA bundle from $URL -> $DEST ($(wc -c <"$DEST") bytes)"
