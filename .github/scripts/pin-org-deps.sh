#!/usr/bin/env bash
# Stock Go cannot resolve the v0.0.0 placeholder that an org module keeps in go.mod.
set -euo pipefail

branch="${GITHUB_HEAD_REF:-${GITHUB_REF_NAME:?GITHUB_REF_NAME is not set}}"
export GOPRIVATE='github.com/wow-look-at-my/*'
if [ -n "${GITHUB_ENV:-}" ]; then echo "GOPRIVATE=$GOPRIVATE" >> "$GITHUB_ENV"; fi

mods="$(go mod edit -json | jq -r '(.Require // [])[] | select(.Path | startswith("github.com/wow-look-at-my/")) | select(.Version | test("^v[0-9]+\\.0\\.0$")) | .Path')"
if [ -z "$mods" ]; then
	echo "no org module placeholders in go.mod"
	exit 0
fi

# A version query outside a module does not load this module's graph, where the other placeholders still fail.
outside="$(mktemp -d)"
for mod in $mods; do
	# A dependency follows the branch of the same name when it has one, and its default branch otherwise.
	if version="$(cd "$outside" && go list -m -f '{{.Version}}' "$mod@$branch" 2>/dev/null)"; then
		echo "$mod: branch $branch -> $version"
	else
		version="$(cd "$outside" && go list -m -f '{{.Version}}' "$mod@HEAD")"
		echo "$mod: default branch -> $version"
	fi
	go mod edit -require="$mod@$version"
	# A v0.0.0-<stamp>-<hash> pseudo-version sorts below the v0.0.0 placeholder.
	go mod edit -replace="$mod=$mod@$version"
done
rm -rf "$outside"

go mod tidy
