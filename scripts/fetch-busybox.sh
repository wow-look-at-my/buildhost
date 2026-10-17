#!/usr/bin/env bash
# Fetch the static busybox binaries that get embedded into buildhost and from
# there into every OCI image buildhost synthesizes from an APE artifact. An APE
# is not a native Linux ELF -- the kernel cannot exec one directly inside a
# scratch/distroless container -- so buildhost ships a static /bin/sh in the
# image's rootfs and runs the binary through it (the APE's own shell prologue
# then assimilates a native ELF on first run). The shell must be static and
# self-contained: there is no glibc or musl in the image other than the bytes we
# put there, and there is no other interpreter to lean on.
#
# The binaries are fetched from Alpine Linux's busybox-static package at build
# time rather than committed, for the same reason the CA bundle in
# internal/repackage/cacerts is fetched and not committed: a 1 MB stripped
# executable is a binary the repo cannot eyeball and is easy to tamper with in a
# PR. Like fetch-cacerts.sh, CI runs this during the build and go-toolchain
# materializes it via a //go:generate directive.
#
# Pinning the version is what makes the embedded shell deterministic and the
# synthesized layers byte-stable (a digest change here changes every synthesized
# image). Bump BUSYBOX_VERSION deliberately, and it must exist for both arches
# below (x86_64 and aarch64) or the build fails loudly.
set -euo pipefail

DEST_DIR="${1:-internal/repackage/shell}"
BUSYBOX_VERSION="${BUSYBOX_VERSION:-1.37.0-r31}"
# Pinned to the Alpine release that carries BUSYBOX_VERSION: latest-stable drops
# old package revisions on every busybox bump, which would break old checkouts.
ALPINE_URL="${ALPINE_URL:-https://dl-cdn.alpinelinux.org/alpine/v3.24}"

# The alpine main repo index for each linux arch maps the busybox-static package
# to an <os>-<arch> filename buildhost keys the embedded shell by, so the layer
# builder can pick the right binary for a target platform's image.
#   x86_64  -> linux/amd64
#   aarch64 -> linux/arm64
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
