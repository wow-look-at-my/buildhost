# What the buildhost-cache-save and buildhost-cache-restore composites must have
# done to a real server. The workflow saves a copy of SENT outside the
# workspace and a directory inside it under SAVED_KEY, twice. It restores them
# by their exact key and moves the results to EXACT and EXACT_WORKSPACE. It
# restores by a restore key into PREFIX, and tries a key nothing holds at MISS.
# The *_HIT and PREFIX_MATCHED variables are the restore steps' outputs. LOG is
# the server's log and BASE its host.
#
# see docs/cache.md

tests:
	- desc: an exact restore is a hit, and puts back every saved path, inside the workspace and out
	  cmd: |
		set -eu
		echo "hit=$EXACT_HIT"
		diff -r --no-dereference "$SENT" "$EXACT"
		cat "$EXACT_WORKSPACE/note"
		echo "trees-identical"
	  outputs:
		stdout:
			- "hit=true"
			- "workspace"
			- "trees-identical"

	- desc: a restore key restores the entry its prefix matches, and is not a hit
	  cmd: |
		set -eu
		echo "hit=$PREFIX_HIT"
		test "$PREFIX_MATCHED" = "$SAVED_KEY"
		diff -r --no-dereference "$SENT" "$PREFIX"
		echo "trees-identical"
	  outputs:
		stdout:
			- "hit=false"
			- "trees-identical"

	- desc: a miss is no hit and creates nothing
	  cmd: |
		set -eu
		echo "hit=$MISS_HIT"
		test ! -e "$MISS"
		echo "nothing-created"
	  outputs:
		stdout:
			- "hit=false"
			- "nothing-created"

	- desc: the second save of the key stored nothing
	  cmd: |
		set -eu
		echo "stored=$(grep -c 'cache entry stored' "$LOG" || true)"
	  outputs:
		stdout:
			- "stored=1"

	- desc: a request that names another repository than its token is refused
	  cmd: |
		set -eu
		token="$(curl -fsS -H "Authorization: Bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=$BASE" | jq -r .value)"
		curl -sS -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $token" \
			"$BASE/api/v1/cache?repository=someone/else&key=$SAVED_KEY"
	  outputs:
		stdout:
			- "403"
