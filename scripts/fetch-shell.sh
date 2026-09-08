#!/usr/bin/env bash
# Fetch the busybox that gets embedded into the buildhost binary, and from there
# into the base layer of every OCI image buildhost synthesizes. It is fetched at
# build time rather than committed, for the same reason the CA bundle is: the
# repo never carries a megabyte of binary a reviewer cannot eyeball.
#
# Fetching it at PULL time instead put Docker Hub in the path of every image this
# server serves, so a deployment that could not reach it served none.
#
# Run this before `go-toolchain` / `go build` locally; the build job runs it as a
# generate directive, and a job that builds directly runs this script.
set -euo pipefail

DEST="${1:-internal/repackage/shell}"

go run ./internal/repackage/shellgen "$DEST"
