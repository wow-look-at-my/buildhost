#!/bin/sh
set -eu

mods="$(go mod edit -json | jq -r '
	.Require[]?
	| select(.Path | startswith("github.com/wow-look-at-my/"))
	| select(.Version // "" | test("^v[0-9]+\\.0\\.0$"))
	| .Path' | sort -u)"

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

for mod in $mods; do
	# The module proxy answers a branch query, and has no answer for @HEAD.
	version="$(cd "$scratch" && GO111MODULE=on GOFLAGS= go list -m -f '{{.Version}}' "$mod@master")"
	if [ -z "$version" ]; then
		echo "resolve-org-deps: no version for $mod@master" >&2
		exit 1
	fi
	go mod edit -require="$mod@$version"
	echo "resolve-org-deps: $mod -> $version"
done
go mod tidy
