# A small artifact fits under the server's advertised direct-upload limit, so
# the upload composite must PUT it whole. Runs before the chunked upload, so
# the session endpoints must be untouched.
#
# The workflow runs the composite and hands the server log here as $LOG.
#
# see docs/uploads.md

tests:
	- desc: a direct-path upload never touches the session endpoints
	  cmd: |
		set -eu
		echo "session-lines=$(grep -c 'path=/api/v1/uploads' "$LOG" || true)"
		grep 'path=/api/v1/uploads' "$LOG" || true
	  outputs:
		stdout:
			- "session-lines=0"
