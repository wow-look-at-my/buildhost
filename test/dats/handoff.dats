# What the buildhost-handoff composite must have done to a real server. The
# workflow sends SENT and receives it into RECEIVED with the job's own OIDC
# token, then hands the results here. LOG is the server's log and BASE its
# host. ACTIONS_ID_TOKEN_REQUEST_* let a case mint a token of its own.
#
# see docs/run-files.md

tests:
	- desc: the received tree is the sent tree, byte for byte, symlinks included
	  cmd: |
		set -eu
		diff -r --no-dereference "$SENT" "$RECEIVED"
		echo "trees-identical"
	  outputs:
		stdout:
			- "trees-identical"

	- desc: the exec bit and the symlink survive the round trip
	  cmd: |
		set -eu
		test -x "$RECEIVED/bin/go"
		test -L "$RECEIVED/compile"
		test "$(readlink "$RECEIVED/compile")" = "../bin/go"
		echo "modes-kept"
	  outputs:
		stdout:
			- "modes-kept"

	- desc: a send past the direct limit went through one upload session
	  cmd: |
		set -eu
		echo "creates=$(grep -c 'method=POST path=/api/v1/uploads ' "$LOG" || true)"
		grep -q 'run file stored' "$LOG"
		echo "stored"
	  outputs:
		stdout:
			- "creates=1"
			- "stored"

	- desc: a name the run does not hold is a 404 that names what the run holds
	  cmd: |
		set -eu
		token="$(curl -fsS -H "Authorization: Bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=$BASE" | jq -r .value)"
		curl -sS -o body.json -w '%{http_code}\n' -H "Authorization: Bearer $token" \
			"$BASE/api/v1/run-files/missing?repository=$GITHUB_REPOSITORY&run_id=$GITHUB_RUN_ID"
		cat body.json
	  outputs:
		stdout:
			- "404"
			- "it holds: toolchain-e2e"

	- desc: a request that names another run than its token is refused
	  cmd: |
		set -eu
		token="$(curl -fsS -H "Authorization: Bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=$BASE" | jq -r .value)"
		curl -sS -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $token" \
			"$BASE/api/v1/run-files/toolchain-e2e?repository=$GITHUB_REPOSITORY&run_id=1"
	  outputs:
		stdout:
			- "403"
