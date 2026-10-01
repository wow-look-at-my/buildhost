#!/usr/bin/env bash
# Print the documented Homebrew flow, localized for a test instance.
#
# Usage: brew-doc-flows.sh public|private <host:port>
#
# This is an extractor, not a test: it applies the ONLY rewrites allowed
# between the docs and what CI runs -- https becomes http, and the documented
# public host becomes the local one. The assertions about what it prints live
# in test/dats/homebrew-*.dats.
set -euo pipefail

leg="${1:?usage: brew-doc-flows.sh public|private <host:port>}"
local_host="${2:?usage: brew-doc-flows.sh public|private <host:port>}"
doc_host="brew.pazer.build"
readme="$(dirname "$0")/../README.md"

case "$leg" in
	public) section='^## Homebrew$'; stop='^#{1,3} ' ;;
	private) section='^### Private projects$'; stop='^#{1,3} ' ;;
	*) echo "unknown leg $leg: want public or private" >&2; exit 1 ;;
esac

block="$(awk -v section="$section" -v stop="$stop" '
	!inside { if ($0 ~ section) { inside = 1 } ; next }
	inside && !fence && $0 ~ stop { exit }
	$0 == "```bash" { fence = 1; next }
	fence && $0 == "```" { exit }
	fence { print }
' "$readme")"

test -n "$block" || { echo "README.md: no fenced bash block for the $leg flow" >&2; exit 1; }
printf '%s\n' "$block" | sed -e 's|https://|http://|g' -e "s|$doc_host|$local_host|g"
