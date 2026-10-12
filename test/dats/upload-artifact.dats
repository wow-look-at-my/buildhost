# What the upload composites must have done to a real server: one
# session for the chunked path, bytes that survive the round trip, and a
# release that ends up published.
# The workflow runs the composites -- an action only runs inside a workflow --
# and hands the results here: LOG is the server's log, BASE and STATIC its
# two hosts, and the SHA and VERSION values are what the steps reported.
#
# This half runs after the artifact uploads and the release publish.
#
# see docs/uploads.md

tests:
	- desc: the big artifact upload opened one session and appended four chunks
	  cmd: |
		set -eu
		echo "creates=$(grep -c 'method=POST path=/api/v1/uploads ' "$LOG" || true)"
		echo "chunks=$(grep -c 'method=PATCH path=/api/v1/uploads/' "$LOG" || true)"
	  outputs:
		stdout:
			- "creates=1"
			- "chunks=4"

	# The sha256 outputs come from the SERVER hashing what it received and
	# stored, so equality with the local files is byte integrity end to end.
	- desc: the server hashed exactly what each path uploaded
	  cmd: |
		set -eu
		test "$SMALL_SHA" = "$(sha256sum "$SMALL_FILE" | awk '{print $1}')"
		test "$BIG_SHA" = "$(sha256sum "$BIG_FILE" | awk '{print $1}')"
		echo "hashes-match"
	  outputs:
		stdout:
			- "hashes-match"

	- desc: the chunk-assembled artifact downloads back byte-identical
	  cmd: |
		set -eu
		curl -fsSL -o big.dl "$STATIC/file?project=upload-e2e&v=$VERSION&os=linux&arch=arm64"
		cmp "$BIG_FILE" big.dl
		echo "bytes-identical"
	  outputs:
		stdout:
			- "bytes-identical"

	- desc: after publish the release is the apex latest
	  cmd: curl -fsS "$BASE/api/v1/projects/upload-e2e/releases/latest"
	  outputs:
		stdout:
			- '"published":true'
