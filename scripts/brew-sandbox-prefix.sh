#!/usr/bin/env bash
# Build a private Homebrew prefix inside a writable directory, and print the environment that points brew at it.
set -euo pipefail

work="${1:?usage: brew-sandbox-prefix.sh <work-dir>}"
src="${BREW_SOURCE:?BREW_SOURCE must name the Homebrew repository to copy from}"

test -x "$src/bin/brew" || {
	echo "BREW_SOURCE=$src holds no bin/brew" >&2
	exit 1
}

prefix="$work/brew"
mkdir -p "$work/home" "$work/cache" "$work/logs" "$work/temp"
mkdir -p "$prefix"
# bin and Library/Homebrew are brew itself, including its vendored ruby. The
# rest of a host prefix is installed packages, which a fresh prefix has none of.
cp -a "$src/bin" "$prefix/bin"
mkdir -p "$prefix/Library"
cp -a "$src/Library/Homebrew" "$prefix/Library/Homebrew"
mkdir -p "$prefix/Library/Taps" "$prefix/Cellar"

printf '%s\n' \
	"export HOME='$work/home'" \
	"export HOMEBREW_CACHE='$work/cache'" \
	"export HOMEBREW_LOGS='$work/logs'" \
	"export HOMEBREW_TEMP='$work/temp'" \
	"export PATH='$prefix/bin:'\$PATH"
