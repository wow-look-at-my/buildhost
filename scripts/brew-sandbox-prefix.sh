#!/usr/bin/env bash
# Build a private Homebrew prefix inside a writable directory, and print the
# environment that points brew at it.
#
# Usage: BREW_SOURCE=<repository> brew-sandbox-prefix.sh <work-dir>
#
# The prefix is a copy of the host's brew source, so tapping, trusting and
# installing all land inside <work-dir>. A dats suite's temp directory is the
# one place its commands may write, and every install format's guarantee here
# is about what brew did to a real file, so the prefix goes there rather than
# the flow going to the host.
#
# BREW_SOURCE names the repository to copy (`brew --repository`). It is
# required: a suite that silently fell back to the host's own prefix would
# install into it, which is exactly what this exists to prevent.
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
# bin and Library/Homebrew are brew itself, including its vendored ruby; the
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
