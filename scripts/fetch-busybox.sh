#!/usr/bin/env bash
# Fetch the static busybox binaries that get embedded into buildhost, and from there into every OCI image it synthesizes from an APE artifact.
set -euo pipefail

DEST_DIR="${1:-internal/repackage/shell}"
BUSYBOX_VERSION="${BUSYBOX_VERSION:-1.37.0-r31}"
# Pinned to the Alpine release that carries BUSYBOX_VERSION: latest-stable drops old package revisions on every busybox bump.
ALPINE_URL="${ALPINE_URL:-https://dl-cdn.alpinelinux.org/alpine/v3.24}"

# Each linux arch maps busybox-static to an <os>-<arch> filename buildhost
# keys the embedded shell by. That lets the layer builder pick the right
# binary per target platform. x86_64 -> linux/amd64 aarch64 -> linux/arm64
fetch_one() {
  local alpine_arch="$1"
  local out_name="$2"
  local pkg="$DEST_DIR/.busybox-${alpine_arch}.apk"
  local extracted="$DEST_DIR/.busybox-${alpine_arch}.d"

  mkdir -p "$DEST_DIR"
  curl -fsSL --retry 3 --proto '=https' -o "$pkg" \
    "$ALPINE_URL/main/${alpine_arch}/busybox-static-${BUSYBOX_VERSION}.apk"
  rm -rf "$extracted"
  mkdir -p "$extracted"
  tar -xzf "$pkg" -C "$extracted" data.tar.gz 2>/dev/null || tar -xzf "$pkg" -C "$extracted"
  mv "$extracted/bin/busybox.static" "$DEST_DIR/$out_name"
  rm -rf "$extracted" "$pkg"
  echo "fetched busybox-static ${BUSYBOX_VERSION} (${alpine_arch}) -> ${DEST_DIR}/${out_name} ($(wc -c <"$DEST_DIR/$out_name") bytes)"
}

fetch_one "x86_64"  "busybox.linux.amd64"
fetch_one "aarch64" "busybox.linux.arm64"
